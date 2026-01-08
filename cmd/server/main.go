package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	_ "time/tzdata"

	"tronbyt-server/internal/auth"
	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/gitutils"
	"tronbyt-server/internal/migration"
	"tronbyt-server/internal/server"

	"github.com/quic-go/quic-go/http3"
	"github.com/tronbyt/pixlet/runtime"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/prometheus"
)

func runHealthCheck(url string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Error("Failed to close health check response body", "error", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status code: %d", resp.StatusCode)
	}

	return nil
}

func openDB(dsn, logLevel string) (*gorm.DB, error) {
	var db *gorm.DB
	var err error

	var gormLogLevel logger.LogLevel
	switch strings.ToUpper(logLevel) {
	case "DEBUG":
		gormLogLevel = logger.Info // GORM Info includes SQL queries
	case "INFO":
		gormLogLevel = logger.Warn
	case "WARN", "WARNING":
		gormLogLevel = logger.Error
	case "ERROR":
		gormLogLevel = logger.Error
	default:
		gormLogLevel = logger.Warn
	}

	gormConfig := &gorm.Config{
		Logger:      data.NewGORMSlogLogger(gormLogLevel, 200*time.Millisecond, true),
		PrepareStmt: false, // Disable prepared statement caching to avoid SQLite locking issues
	}

	if strings.HasPrefix(dsn, "postgres") || strings.Contains(dsn, "host=") {
		slog.Info("Using Postgres DB")
		db, err = gorm.Open(postgres.Open(dsn), gormConfig)
	} else if strings.Contains(dsn, "@tcp(") || strings.Contains(dsn, "@unix(") {
		slog.Info("Using MySQL DB")
		db, err = gorm.Open(mysql.Open(dsn), gormConfig)
	} else {
		slog.Info("Using SQLite DB", "path", dsn)
		db, err = gorm.Open(sqlite.Open(dsn), gormConfig)
		if err == nil {
			if err := db.Exec("PRAGMA journal_mode=WAL;").Error; err != nil {
				slog.Warn("Failed to set WAL mode for SQLite", "error", err)
			}
			if err := db.Exec("PRAGMA busy_timeout=5000;").Error; err != nil {
				slog.Warn("Failed to set busy timeout for SQLite", "error", err)
			}
		}
	}

	if err == nil {
		if err := db.Use(prometheus.New(prometheus.Config{
			DBName:          "tronbyt",
			RefreshInterval: 15,
			StartServer:     false,
			HTTPServerPort:  8080,
		})); err != nil {
			slog.Warn("Failed to register GORM prometheus plugin", "error", err)
		}
	}

	return db, err
}

func resetPassword(dsn, username, password string) error {
	db, err := openDB(dsn, "INFO")
	if err != nil {
		return err
	}

	hashedPassword, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	rowsAffected, err := gorm.G[data.User](db).Where("username = ?", username).Update(context.Background(), "password", hashedPassword)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
}

func sanitizeDB(db *gorm.DB) {
	// Sanitize data before migration (fixes v2.0.x empty email constraint issue)
	if db.Migrator().HasTable(&data.User{}) {
		if _, err := gorm.G[data.User](db).Where("email IN ?", []string{"", "none"}).Update(context.Background(), "email", nil); err != nil {
			slog.Warn("Failed to sanitize empty emails", "error", err)
		}
	}

	// Fix timezone issues
	if db.Migrator().HasTable(&data.Device{}) {
		devices, err := gorm.G[data.Device](db).Where("location LIKE '%\"timezone\":\"None\"'").Find(context.Background())
		if err != nil {
			slog.Warn("Failed to get devices with illegal timestamps", "error", err)
		} else {
			for _, device := range devices {
				device.Location.Timezone = ""
				if _, err := gorm.G[data.Device](db).Where("id = ?", device.ID).Update(context.Background(), "location", device.Location); err != nil {
					slog.Warn("Failed to update device location during sanitization", "device_id", device.ID, "error", err)
				}
			}
		}
	}
}

func main() {
	// Initialize slog before anything else that might log
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	// Load configuration early to get default DB path
	cfg, err := config.LoadSettings()
	if err != nil {
		slog.Error("Failed to load settings", "error", err)
		os.Exit(1)
	}

	// Re-initialize logger with configured log level
	var level slog.Level
	switch strings.ToUpper(cfg.LogLevel) {
	case "DEBUG":
		level = slog.LevelDebug
	case "INFO":
		level = slog.LevelInfo
	case "WARN", "WARNING":
		level = slog.LevelWarn
	case "ERROR":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
		slog.Warn("Invalid LOG_LEVEL, defaulting to INFO", "level", cfg.LogLevel)
	}

	// Create handler options with the parsed level
	handlerOpts := &slog.HandlerOptions{
		Level: level,
	}
	logger = slog.New(slog.NewTextHandler(os.Stdout, handlerOpts))
	slog.SetDefault(logger)
	slog.Debug("Logger initialized", "level", level)

	dbDSN := flag.String("db", cfg.DBDSN, "Database DSN (sqlite file path or connection string)")
	dataDir := flag.String("data", cfg.DataDir, "Path to data directory")
	flag.Parse()

	if len(flag.Args()) > 0 {
		cmd := flag.Arg(0)
		switch cmd {
		case "health":
			url := "http://localhost:8000/health"
			if len(flag.Args()) > 1 {
				url = flag.Arg(1)
			}
			if err := runHealthCheck(url); err != nil {
				fmt.Fprintf(os.Stderr, "Health check failed: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		case "reset-password":
			if len(flag.Args()) < 3 {
				fmt.Println("Usage: tronbyt-server reset-password <username> <new_password>")
				os.Exit(1)
			}
			username := flag.Arg(1)
			password := flag.Arg(2)
			if err := resetPassword(*dbDSN, username, password); err != nil {
				slog.Error("Failed to reset password", "error", err)
				os.Exit(1)
			}
			slog.Info("Password reset successfully")
			os.Exit(0)
		}
	}

	if err := os.MkdirAll(cfg.DataDir, 0755); err != nil {
		slog.Error("Failed to create data directory", "error", err)
		os.Exit(1)
	}

	// Check for legacy DB for automatic migration
	legacyDBPath := filepath.Join("users", "usersdb.sqlite") // Old Python DB path
	if _, err := os.Stat(legacyDBPath); err == nil && legacyDBPath != *dbDSN {
		slog.Info("Found legacy database, checking if migration is needed", "legacy_db", legacyDBPath, "new_db", *dbDSN)

		skipMigration := false
		tempDB, err := openDB(*dbDSN, "ERROR")
		if err == nil {
			if tempDB.Migrator().HasTable(&data.User{}) {
				count, err := gorm.G[data.User](tempDB).Count(context.Background(), "*")
				if err == nil && count > 0 {
					skipMigration = true
					slog.Warn("New database already has users, skipping automatic migration.", "new_db", *dbDSN)
				}
			}
			if sqlDB, err := tempDB.DB(); err == nil {
				if err := sqlDB.Close(); err != nil {
					slog.Error("Failed to close temporary DB connection", "error", err)
				}
			}
		}

		if !skipMigration {
			// Perform migration
			if err := migration.MigrateLegacyDB(legacyDBPath, *dbDSN, *dataDir); err != nil {
				slog.Error("Automatic migration failed", "error", err)
				os.Exit(1)
			}
			slog.Info("Automatic migration completed successfully. Renaming legacy DB.", "legacy_db", legacyDBPath)
			if err := os.Rename(legacyDBPath, legacyDBPath+".bak"); err != nil {
				slog.Error("Failed to rename legacy DB after migration", "error", err)
			}
		}
	}

	// Clone/Update System Apps Repo
	systemAppsDir := filepath.Join(*dataDir, "system-apps")
	shouldUpdate := cfg.Production == "1"
	if err := gitutils.EnsureRepo(systemAppsDir, cfg.SystemAppsRepo, cfg.GitHubToken, shouldUpdate); err != nil {
		slog.Error("Failed to update system apps repo", "error", err)
		// Continue anyway
	}

	// Open DB
	db, err := openDB(*dbDSN, cfg.LogLevel)
	if err != nil {
		slog.Error("Failed to open database", "error", err)
		os.Exit(1)
	}

	// Sanitize data
	sanitizeDB(db)

	// AutoMigrate (ensure schema exists)
	if err := db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.WebAuthnCredential{}, &data.Setting{}); err != nil {
		slog.Error("Failed to migrate schema", "error", err)
		os.Exit(1)
	}

	// Initialize Pixlet Cache
	var cache runtime.Cache
	if cfg.RedisURL != "" {
		slog.Info("Initializing Pixlet Redis cache", "url", cfg.RedisURL)
		cache = runtime.NewRedisCache(cfg.RedisURL)
	} else {
		slog.Info("Initializing Pixlet in-memory cache")
		cache = runtime.NewInMemoryCache()
	}
	runtime.InitHTTP(cache)
	runtime.InitCache(cache)

	srv := server.NewServer(db, cfg)
	srv.DataDir = *dataDir
	srv.Config = cfg // Pass the loaded configuration

	// Firmware Update (production only)
	if cfg.Production == "1" {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Error("Panic during background firmware update", "panic", r)
				}
			}()
			if err := srv.UpdateFirmwareBinaries(); err != nil {
				slog.Error("Failed to update firmware binaries in background", "error", err)
			}
		}()
	} else {
		slog.Info("Skipping firmware update (dev mode)")
	}

	// Single User Auto-Login Warning
	userCount, err := gorm.G[data.User](db).Count(context.Background(), "*")
	if err != nil {
		slog.Error("Failed to count users for auto-login warning", "error", err)
	} else if cfg.SingleUserAutoLogin == "1" && userCount == 1 {
		slog.Warn(`
======================================================================
⚠️  SINGLE-USER AUTO-LOGIN MODE IS ENABLED
======================================================================
Authentication is DISABLED for private network connections!

This mode automatically logs in the single user without password.

SECURITY REQUIREMENTS:
  ✓ Only works when exactly 1 user exists
  ✓ Only works from trusted networks:
    - Localhost (127.0.0.1, ::1)
    - Private IPv4 networks (192.168.x.x, 10.x.x.x, 172.16.x.x)
    - IPv6 local ranges (Unique Local Addresses fc00::/7, commonly fd00::/8)
    - IPv6 link-local (fe80::/10)
  ✓ Public IP connections still require authentication

To disable: Set SINGLE_USER_AUTO_LOGIN=0 in your .env file
======================================================================`)
	}

	// Configure Listeners
	var listeners []net.Listener

	// TCP
	if cfg.Host != "" || cfg.Port != "" {
		addr := net.JoinHostPort(cfg.Host, cfg.Port)
		l, err := net.Listen("tcp", addr)
		if err != nil {
			slog.Error("Failed to listen on TCP", "addr", addr, "error", err)
			os.Exit(1)
		}
		listeners = append(listeners, l)
		slog.Info("Listening on TCP", "addr", addr)
	}

	// Unix Socket
	if cfg.UnixSocket != "" {
		if err := os.RemoveAll(cfg.UnixSocket); err != nil {
			slog.Warn("Failed to remove old socket", "error", err)
		}
		l, err := net.Listen("unix", cfg.UnixSocket)
		if err != nil {
			slog.Error("Failed to listen on Unix socket", "path", cfg.UnixSocket, "error", err)
			os.Exit(1)
		}
		if err := os.Chmod(cfg.UnixSocket, 0666); err != nil {
			slog.Warn("Failed to set socket permissions", "error", err)
		}
		listeners = append(listeners, l)
		slog.Info("Listening on Unix socket", "path", cfg.UnixSocket)
	}

	if len(listeners) == 0 {
		slog.Error("No listeners configured")
		os.Exit(1)
	}

	var handler http.Handler = srv

	// Determine number of servers to start and create error channel
	numServers := len(listeners)
	http3Enabled := cfg.SSLCertFile != "" && cfg.SSLKeyFile != "" && (cfg.Host != "" || cfg.Port != "")
	if http3Enabled {
		numServers++
	}
	errCh := make(chan error, numServers)

	// TLS Config (Shared)
	var tlsConfig *tls.Config
	if cfg.SSLCertFile != "" && cfg.SSLKeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.SSLCertFile, cfg.SSLKeyFile)
		if err != nil {
			slog.Error("Failed to load TLS certificates", "error", err)
			os.Exit(1)
		}
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"h2", "http/1.1"},
		}
		slog.Info("TLS certificates loaded", "cert", cfg.SSLCertFile, "key", cfg.SSLKeyFile)
	}

	// HTTP/3 (QUIC) Support
	if http3Enabled {
		if tlsConfig == nil {
			// This should practically not happen due to http3Enabled check above,
			// but good for safety if logic changes.
			slog.Error("HTTP/3 enabled but TLS config is nil")
			os.Exit(1)
		}
		addr := net.JoinHostPort(cfg.Host, cfg.Port)
		h3Srv := &http3.Server{
			Addr:        addr,
			Handler:     srv,
			IdleTimeout: 120 * time.Second,
			TLSConfig:   tlsConfig,
		}

		go func() {
			slog.Info("Serving HTTP/3 (QUIC)", "addr", addr)
			err := h3Srv.ListenAndServe()
			if err != nil && err != http.ErrServerClosed {
				errCh <- fmt.Errorf("HTTP/3 server failed: %w", err)
			}
		}()

		// Wrap handler to set Alt-Svc header for TCP connections
		baseHandler := handler
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Determine the external port to advertise in Alt-Svc.
			// Use GetBaseURL to handle X-Forwarded-Host/Proto respecting logic centrally.
			baseURL := srv.GetBaseURL(r)
			u, err := url.Parse(baseURL)
			var port string
			if err == nil {
				port = u.Port()
			}

			if port == "" {
				// No port in URL, use default based on scheme
				// HTTP/3 implies HTTPS, so 443.
				port = "443"
			}

			w.Header().Set("Alt-Svc", fmt.Sprintf(`h3=":%s"; ma=2592000`, port))
			baseHandler.ServeHTTP(w, r)
		})
	}

	httpSrv := &http.Server{
		Handler:      handler,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	slog.Info("Tronbyt Server starting", "db", *dbDSN, "dataDir", *dataDir)

	for _, l := range listeners {
		go func(l net.Listener) {
			var err error
			if tlsConfig != nil {
				// Wrap listener with TLS if config is present
				l = tls.NewListener(l, tlsConfig)
			}
			err = httpSrv.Serve(l)
			if err != nil && err != http.ErrServerClosed {
				errCh <- err
			}
		}(l)
	}

	if err := <-errCh; err != nil {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	}
}
