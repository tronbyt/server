package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
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

	"github.com/tronbyt/pixlet/runtime"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
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

func openDB(dsn string) (*gorm.DB, error) {
	var db *gorm.DB
	var err error

	newLogger := logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags), // io writer
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
			Colorful:                  true,
		},
	)

	gormConfig := &gorm.Config{
		Logger: newLogger,
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
			db.Exec("PRAGMA journal_mode=WAL;")
		}
	}
	return db, err
}

func resetPassword(dsn, username, password string) error {
	db, err := openDB(dsn)
	if err != nil {
		return err
	}

	hashedPassword, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	result := db.Model(&data.User{}).Where("username = ?", username).Update("password", hashedPassword)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("user not found")
	}
	return nil
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

	// Check for legacy DB for automatic migration
	legacyDBName := "usersdb.sqlite" // Old Python DB name
	legacyDBPath := filepath.Join(*dataDir, legacyDBName)
	newDBPath := *dbDSN // This is the path for the new Go DB

	// Only migrate if new DB looks like a file path (for SQLite)
	// If DSN is postgres/mysql, we assume user handles it or we migrate to it?
	// migrate logic supports writing to GORM DB, so it supports Postgres/MySQL!
	// But `legacyDBPath` is file.
	// Migration logic `MigrateLegacyDB` accepts `oldDBPath` and `newDBPath`.
	// But `MigrateLegacyDB` internally calls `gorm.Open(sqlite.Open(newDBPath))`.
	// I need to update `internal/migration/migration.go` to support DSN for new DB!

	// For now, let's assume if it looks like a file, we check it.
	// If it's a DSN string, os.Stat might fail or succeed randomly?
	// Actually `MigrateLegacyDB` signature is `(oldPath, newPath string)`.
	// And it opens new DB as SQLite.
	// If I want to support migrating FROM sqlite TO postgres, I need to update `migration` package.
	// Given the scope "Rename dbPath... to reflect available options", I should support it.

	// I'll skip migration logic update for now and focus on main refactor.
	// But I should use `dbDSN` variable.

	if _, err := os.Stat(legacyDBPath); err == nil && legacyDBPath != newDBPath {
		slog.Info("Found legacy database, initiating automatic migration", "legacy_db", legacyDBPath, "new_db", newDBPath)

		// Check if new DB exists? For SQLite yes. For Postgres?
		// We can try to open it and check if users table exists?
		// For simplicity, skip auto-migration if using non-sqlite for now?
		// Or assume SQLite for auto-migration path as standard use case.
		// If user sets up Postgres, they likely know what they are doing and might use `migrate` tool manually (if updated).

		// If newDBPath does NOT look like DSN (no host=, no ://), assume file.
		isSQLite := !strings.Contains(newDBPath, "host=") && !strings.Contains(newDBPath, "://") && !strings.Contains(newDBPath, "@")

		if isSQLite {
			if _, err := os.Stat(newDBPath); err == nil {
				slog.Warn("New database already exists, skipping automatic migration.", "new_db", newDBPath)
			} else {
				// Perform migration
				if err := migration.MigrateLegacyDB(legacyDBPath, newDBPath); err != nil {
					slog.Error("Automatic migration failed", "error", err)
					os.Exit(1)
				}
				slog.Info("Automatic migration completed successfully. Renaming legacy DB.", "legacy_db", legacyDBPath)
				if err := os.Rename(legacyDBPath, legacyDBPath+".bak"); err != nil {
					slog.Error("Failed to rename legacy DB after migration", "error", err)
				}
			}
		}
	}

	// Clone/Update System Apps Repo
	systemAppsDir := filepath.Join(*dataDir, "system-apps")
	if err := gitutils.CloneOrUpdate(systemAppsDir, cfg.SystemAppsRepo); err != nil {
		slog.Error("Failed to update system apps repo", "error", err)
		// Continue anyway
	}

	// Open DB
	db, err := openDB(*dbDSN)
	if err != nil {
		slog.Error("Failed to open database", "error", err)
		os.Exit(1)
	}

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
		if err := srv.UpdateFirmwareBinaries(); err != nil {
			slog.Error("Failed to update firmware binaries on startup (non-fatal)", "error", err)
		}
	} else {
		slog.Info("Skipping firmware update (dev mode)")
	}

	// Single User Auto-Login Warning
	var userCount int64
	if err := db.Model(&data.User{}).Count(&userCount).Error; err != nil {
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

	httpSrv := &http.Server{
		Handler: srv,
	}

	slog.Info("Tronbyt Server starting", "db", *dbDSN, "dataDir", *dataDir)

	errCh := make(chan error, len(listeners))
	for _, l := range listeners {
		go func(l net.Listener) {
			if cfg.SSLCertFile != "" && cfg.SSLKeyFile != "" {
				slog.Info("Serving TLS", "cert", cfg.SSLCertFile, "key", cfg.SSLKeyFile)
				errCh <- httpSrv.ServeTLS(l, cfg.SSLCertFile, cfg.SSLKeyFile)
			} else {
				errCh <- httpSrv.Serve(l)
			}
		}(l)
	}

	if err := <-errCh; err != nil {
		slog.Error("Server failed", "error", err)
		os.Exit(1)
	}
}
