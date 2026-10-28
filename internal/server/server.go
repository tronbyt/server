package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/chunker"
	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"
	syncer "tronbyt-server/internal/sync"
	"tronbyt-server/internal/version"
	"tronbyt-server/web"

	securejoin "github.com/cyphar/filepath-securejoin"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gorilla/sessions"
	"github.com/gorilla/websocket"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/text/language"
	"gorm.io/gorm"
)

type Server struct {
	installCounts map[string]int64 // public app ID -> Niblet install count
	usageRunning  atomic.Bool      // a community app stats sync is in progress
	usageForce    atomic.Bool      // a consent change awaits a sync that ignores the retry wait
	usageSyncs    sync.WaitGroup   // background syncs started by a consent change
	DB            *gorm.DB
	Router        *http.ServeMux
	DataDir       string
	BaseTemplates *template.Template
	PageTemplates map[string]*template.Template
	Config        *config.Settings
	Store         *sessions.CookieStore
	Bundle        *i18n.Bundle // Add i18n bundle
	Broadcaster   *syncer.Broadcaster
	Upgrader      *websocket.Upgrader
	PromRegistry  prometheus.Registerer
	PromGatherer  prometheus.Gatherer
	metrics       *appMetrics
	OIDCProvider  *OIDCProvider

	systemAppsCache                      []apps.AppMetadata
	systemAppsCacheMutex                 sync.RWMutex
	systemAppsRefreshMutex               sync.Mutex
	systemAppsAutoRefreshPreferenceMutex sync.Mutex
	systemAppsAutoRefresh                atomic.Bool
	nibletCloudPreferenceMutex           sync.Mutex
	nibletCloudEnabled                   atomic.Bool

	appChunksCache        map[string][]chunker.RenderChunk
	appChunksMu           sync.RWMutex
	deviceChunkIndices    map[string]int
	deviceCurrentChunkApp map[string]string
	deviceChunkMu         sync.Mutex

	// SchemaCache, when set, allows forcing a one-shot refetch of an app's
	// cached HTTP responses so dynamic schema data (e.g. dropdown options
	// fetched in get_schema) can be refreshed before the app's TTL expires.
	SchemaCache SchemaCacheBypasser

	UpdateAvailable  bool
	LatestReleaseURL string

	// githubAPIBase, when set, replaces https://api.github.com (used by tests).
	githubAPIBase string
}

// SchemaCacheBypasser forces cache reads for keys with the given prefix to miss
// for the duration of fn, so http.get calls made during fn refetch from origin.
type SchemaCacheBypasser interface {
	WithBypass(prefix string, fn func() error) error
}

// Map template names to their file paths relative to web/templates.
var templateFiles = map[string]string{
	"index":            "manager/index.html",
	"adminindex":       "manager/adminindex.html",
	"login":            "auth/login.html",
	"register":         "auth/register.html",
	"edit":             "auth/edit.html",
	"settings_account": "settings/account.html",
	"settings_content": "settings/content.html",
	"settings_admin":   "settings/admin.html",
	"create":           "manager/create.html",
	"addapp":           "manager/addapp.html",
	"configapp":        "manager/configapp.html",
	"uploadapp":        "manager/uploadapp.html",
	"firmware":         "manager/firmware.html",
	"update":           "manager/update.html",
	"device_tv":        "manager/device_tv.html",
	"settings":         "admin/settings.html",
}

// NewServer creates and initializes a new Server instance with the provided database and settings.
func NewServer(db *gorm.DB, cfg *config.Settings) *Server {
	version.AllowRemoteLookup = cfg.EnableUpdateChecks
	version.EnsureGitInfo()

	s := &Server{
		DB:          db,
		Router:      http.NewServeMux(),
		DataDir:     cfg.DataDir,
		Config:      cfg,
		Bundle:      i18n.NewBundle(language.English), // Default language
		Broadcaster: syncer.NewBroadcaster(),
		Upgrader: &websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		PromRegistry:          prometheus.DefaultRegisterer,
		PromGatherer:          prometheus.DefaultGatherer,
		appChunksCache:        make(map[string][]chunker.RenderChunk),
		deviceChunkIndices:    make(map[string]int),
		deviceCurrentChunkApp: make(map[string]string),
	}

	// Load Settings from DB
	// Secret Key
	secretKey, err := s.getSetting("secret_key")
	if err != nil || secretKey == "" {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			slog.Error("Failed to generate random secret key", "error", err)
			// Fallback to avoid crash, though this is critical
			secretKey = "insecure-fallback-key-" + fmt.Sprintf("%d", time.Now().UnixNano())
		} else {
			secretKey = base64.StdEncoding.EncodeToString(b)
		}
		if err := s.setSetting("secret_key", secretKey); err != nil {
			slog.Error("Failed to save secret key to settings", "error", err)
		}
	}

	// System Repo
	repo, err := s.getSetting("system_apps_repo")
	if err == nil && repo != "" {
		cfg.SystemAppsRepo = repo
	}
	if val, err := s.getSetting(systemAppsAutoRefreshSettingKey); err != nil {
		slog.Warn("Failed to load system apps auto-refresh setting", "error", err)
	} else if val != "" {
		cfg.SystemAppsAutoRefresh = val == "true"
	}
	s.systemAppsAutoRefresh.Store(cfg.SystemAppsAutoRefresh)

	s.loadNibletCloudEnabled()

	// Load OIDC settings from DB
	if val, err := s.getSetting("oidc_enabled"); err == nil {
		cfg.OIDCEnabled = val == "true"
	}
	if val, err := s.getSetting("oidc_issuer_url"); err == nil {
		cfg.OIDCIssuerURL = val
	}
	if val, err := s.getSetting("oidc_client_id"); err == nil {
		cfg.OIDCClientID = val
	}
	if val, err := s.getSetting("oidc_client_secret"); err == nil {
		cfg.OIDCClientSecret = val
	}
	if val, err := s.getSetting("oidc_additional_scopes"); err == nil {
		cfg.OIDCAdditionalScopes = val
	}
	if val, err := s.getSetting("oidc_allow_auto_create"); err == nil {
		cfg.OIDCAllowAutoCreate = val == "true"
	}
	if val, err := s.getSetting("oidc_admin_group_claim"); err == nil {
		cfg.OIDCAdminGroupClaim = val
	}
	if val, err := s.getSetting("oidc_admin_group_value"); err == nil {
		cfg.OIDCAdminGroupValue = val
	}
	if val, err := s.getSetting("oidc_username_claim"); err == nil {
		cfg.OIDCUsernameClaim = val
	}

	// Initialize OIDC provider
	if cfg.OIDCEnabled && cfg.OIDCIssuerURL != "" {
		prov, err := s.setupOIDCProvider(context.Background())
		if err != nil {
			slog.Warn("Failed to initialize OIDC provider, OIDC login disabled", "error", err)
			s.OIDCProvider = nil
		} else {
			s.OIDCProvider = prov
			slog.Info("OIDC provider initialized", "issuer", cfg.OIDCIssuerURL)
		}
	}

	s.Store = sessions.NewCookieStore([]byte(secretKey))

	// Configure Session Store
	s.Store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 30,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}

	gob.Register(webauthn.SessionData{})

	// Load translations
	s.Bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	if entries, err := web.Assets.ReadDir("i18n"); err != nil {
		slog.Error("Failed to read i18n directory", "error", err)
	} else {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
				path := "i18n/" + entry.Name()
				data, err := web.Assets.ReadFile(path)
				if err != nil {
					slog.Error("Failed to read translation file", "file", entry.Name(), "error", err)
					continue
				}
				if _, err := s.Bundle.ParseMessageFileBytes(data, entry.Name()); err != nil {
					slog.Error("Failed to parse translation file", "file", entry.Name(), "error", err)
				}
			}
		}
	}

	funcMap := getFuncMap()

	// Load base templates and partials
	s.BaseTemplates = template.New("").Funcs(funcMap)
	// Parse base and partials
	if _, err := s.BaseTemplates.ParseFS(web.Assets, "templates/base.html", "templates/partials/*.html"); err != nil {
		slog.Error("Failed to parse base templates", "error", err)
	}

	s.PageTemplates = make(map[string]*template.Template)
	for name, path := range templateFiles {
		tmpl, err := s.BaseTemplates.Clone()
		if err != nil {
			slog.Error("Failed to clone template", "name", name, "error", err)
			continue
		}
		if _, err := tmpl.ParseFS(web.Assets, "templates/"+path); err != nil {
			slog.Error("Failed to parse page template", "name", name, "path", path, "error", err)
			continue
		}
		s.PageTemplates[name] = tmpl
	}

	s.registerMetrics()
	s.RefreshSystemAppsCache()

	// Clean up and recreate tmp directory
	tmpDir := s.GetTmpDir()
	if err := os.RemoveAll(tmpDir); err != nil {
		slog.Warn("Failed to clean up tmp directory on startup", "path", tmpDir, "error", err)
	}
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		slog.Error("Failed to create tmp directory on startup", "path", tmpDir, "error", err)
	}

	go s.checkForUpdates(context.Background())
	go s.autoRefreshSystemRepo(context.Background())
	go s.autoRefreshCustomAppsRepos()
	if cfg.NibletCloudURL != "" {
		go s.runNibletSync()
		go s.runUsageSync()
	}

	s.routes()
	return s
}

// routes registers all HTTP endpoints, static assets, and websocket handlers.
func (s *Server) routes() {
	// Conflict Resolution Handlers (Must be registered before conflicting wildcards?)
	// Actually order of registration doesn't matter in Go 1.22 for correctness, but presence matters.
	// But let's put them first for clarity.
	s.Router.HandleFunc("GET /static/ws", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })

	// Static files
	staticFS, err := fs.Sub(web.Assets, "static")
	if err != nil {
		slog.Error("Failed to sub static fs", "error", err)
	} else {
		fileServer := http.FileServer(http.FS(staticFS))
		// Register specific subdirectories to avoid conflict with /{id}/ws
		s.Router.Handle("GET /static/css/", http.StripPrefix("/static/", fileServer))
		s.Router.Handle("GET /static/js/", http.StripPrefix("/static/", fileServer))
		s.Router.Handle("GET /static/webfonts/", http.StripPrefix("/static/", fileServer))
		s.Router.Handle("GET /static/images/", http.StripPrefix("/static/", fileServer))
		s.Router.Handle("GET /static/sounds/", http.StripPrefix("/static/", fileServer))
		s.Router.Handle("GET /static/favicon.ico", http.StripPrefix("/static/", fileServer))
	}

	// Serve firmware binaries
	firmwareDir := filepath.Join(s.DataDir, "firmware")
	s.Router.Handle("GET /static/firmware/", http.StripPrefix("/static/firmware/", http.FileServer(http.Dir(firmwareDir))))

	// Serve custom sound files (files only, audio extensions only, no directory browsing)
	soundsDir := filepath.Join(s.DataDir, "sounds")
	s.Router.HandleFunc("GET /static/custom_sounds/", http.NotFound)
	s.Router.HandleFunc("GET /static/custom_sounds/{filename}", func(w http.ResponseWriter, r *http.Request) {
		rawFilename := r.PathValue("filename")
		filename := filepath.Base(rawFilename)
		if filename == "" || filename == "." || filename != rawFilename {
			http.NotFound(w, r)
			return
		}
		ext := strings.ToLower(filepath.Ext(filename))
		if !slices.Contains(allowedAudioExtensions, ext) {
			http.NotFound(w, r)
			return
		}
		targetPath, err := securejoin.SecureJoin(soundsDir, filename)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		fi, err := os.Stat(targetPath)
		if err != nil || fi.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		http.ServeFile(w, r, targetPath)
	})

	// Unified sound route for apps, custom uploads, and built-in sounds
	s.Router.HandleFunc("GET /sounds/{appId}/{filename}", s.handleUnifiedSound)
	s.Router.HandleFunc("GET /api/sounds/{filename}", s.handleUnifiedSound)

	// App Preview (Specific path)
	s.Router.HandleFunc("GET /preview/app/{id}", s.RequireLogin(s.handleAppThumbnail))

	s.SetupAPIRoutes()
	s.SetupAuthRoutes()

	// Device endpoint
	s.Router.Handle("GET /{id}/next", http.HandlerFunc(s.handleNextApp))

	// Web UI
	s.Router.HandleFunc("GET /", s.RequireLogin(s.handleIndex))
	s.Router.HandleFunc("GET /admin", s.RequireLogin(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/settings/admin", http.StatusSeeOther)
	}))
	s.Router.HandleFunc("GET /settings/admin", s.RequireLogin(s.handleAdminIndex))
	s.Router.HandleFunc("POST /settings/admin/oidc", s.RequireLogin(s.handleAdminSettingsPost))
	s.Router.Handle("POST /settings/admin/usage-sharing", http.NewCrossOriginProtection().Handler(s.RequireLogin(s.handleUsageSharingConsent)))
	s.Router.HandleFunc("GET /.well-known/niblet-reporter", s.handleUsageChallenge)
	s.Router.HandleFunc("GET /admin/settings", s.RequireLogin(s.handleAdminSettingsGet))   // Keep old path just in case
	s.Router.HandleFunc("POST /admin/settings", s.RequireLogin(s.handleAdminSettingsPost)) // Keep old path just in case
	s.Router.HandleFunc("DELETE /admin/users/{username}", s.RequireLogin(s.handleDeleteUser))
	s.Router.HandleFunc("DELETE /settings/admin/users/{username}", s.RequireLogin(s.handleDeleteUser))
	s.Router.HandleFunc("POST /settings/admin/users/{username}/email", s.RequireLogin(s.handleAdminUpdateUserEmail))
	s.Router.HandleFunc("POST /settings/admin/sounds/upload", s.RequireLogin(s.handleAdminUploadSound))
	s.Router.HandleFunc("POST /settings/admin/sounds/{filename}/delete", s.RequireLogin(s.handleAdminDeleteSound))

	s.Router.HandleFunc("GET /devices/create", s.RequireLogin(s.handleCreateDeviceGet))
	s.Router.HandleFunc("POST /devices/create", s.RequireLogin(s.handleCreateDevicePost))
	s.Router.HandleFunc("POST /devices/import", s.RequireLogin(s.handleImportNewDeviceConfig))

	s.Router.HandleFunc("POST /devices/{id}/update_brightness", s.RequireLogin(s.RequireDevice(s.handleUpdateBrightness)))
	s.Router.HandleFunc("POST /devices/{id}/update_volume", s.RequireLogin(s.RequireDevice(s.handleUpdateVolume)))
	s.Router.HandleFunc("POST /devices/{id}/update_interval", s.RequireLogin(s.RequireDevice(s.handleUpdateInterval)))
	s.Router.HandleFunc("POST /devices/{id}/set_night_mode_override", s.RequireLogin(s.RequireDevice(s.handleSetNightModeOverride)))
	s.Router.HandleFunc("POST /devices/{id}/set_dim_mode_override", s.RequireLogin(s.RequireDevice(s.handleSetDimModeOverride)))
	s.Router.HandleFunc("POST /devices/{id}/set_mute", s.RequireLogin(s.RequireDevice(s.handleSetMute)))
	s.Router.HandleFunc("POST /devices/{id}/test_sound", s.RequireLogin(s.RequireDevice(s.handleTestSoundDevice)))

	s.Router.HandleFunc("GET /devices/{id}/addapp", s.RequireLogin(s.RequireDevice(s.handleAddAppGet)))
	s.Router.HandleFunc("POST /devices/{id}/addapp", s.RequireLogin(s.RequireDevice(s.handleAddAppPost)))

	s.Router.HandleFunc("POST /devices/{id}/{iname}/delete", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleDeleteApp))))
	s.Router.HandleFunc("GET /devices/{id}/{iname}/config", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleConfigAppGet))))
	s.Router.HandleFunc("GET /devices/{id}/{iname}/schema", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleAppSchemaGet))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/config", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleConfigAppPost))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/schema_handler/{handler}", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleSchemaHandler))))

	s.Router.HandleFunc("POST /devices/{id}/{iname}/toggle_pin", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleTogglePin))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/toggle_enabled", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleToggleEnabled))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/moveapp", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleMoveApp))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/duplicate", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleDuplicateApp))))
	s.Router.HandleFunc("POST /devices/{id}/apps/duplicate_from/{source_device_id}/{iname}", s.RequireLogin(s.RequireDevice(s.handleDuplicateAppToDevice)))
	s.Router.HandleFunc("POST /devices/{id}/reorder_apps", s.RequireLogin(s.RequireDevice(s.handleReorderApps)))

	s.Router.HandleFunc("GET /devices/{id}/uploadapp", s.RequireLogin(s.RequireDevice(s.handleUploadAppGet)))
	s.Router.HandleFunc("POST /devices/{id}/uploadapp", s.RequireLogin(s.RequireDevice(s.handleUploadAppPost)))
	s.Router.HandleFunc("GET /devices/{id}/uploads/{filename}/delete", s.RequireLogin(s.RequireDevice(s.handleDeleteUpload)))

	s.Router.HandleFunc("POST /set_theme_preference", s.RequireLogin(s.handleSetThemePreference))
	s.Router.HandleFunc("POST /set_user_repo", s.RequireLogin(s.handleSetUserRepo))
	s.Router.HandleFunc("POST /refresh_user_repo", s.RequireLogin(s.handleRefreshUserRepo))

	s.Router.HandleFunc("GET /export_user_config", s.RequireLogin(s.handleExportUserConfig))
	s.Router.HandleFunc("POST /import_user_config", s.RequireLogin(s.handleImportUserConfig))
	s.Router.HandleFunc("GET /devices/{id}/export_config", s.RequireLogin(s.RequireDevice(s.handleExportDeviceConfig)))

	s.Router.HandleFunc("POST /set_system_repo", s.RequireLogin(s.handleSetSystemRepo))
	s.Router.HandleFunc("POST /refresh_system_repo", s.RequireLogin(s.handleRefreshSystemRepo))
	s.Router.HandleFunc("POST /clear_system_apps_repo", s.RequireLogin(s.handleClearSystemAppsRepo))
	s.Router.HandleFunc("POST /settings/system-apps-auto-refresh", s.RequireLogin(s.handleSetSystemAppsAutoRefresh))
	s.Router.HandleFunc("POST /update_firmware", s.RequireLogin(s.handleUpdateFirmware))

	// App broken status (development only)
	s.Router.HandleFunc("POST /mark_app_broken", s.RequireLogin(s.handleMarkAppBroken))
	s.Router.HandleFunc("POST /unmark_app_broken", s.RequireLogin(s.handleUnmarkAppBroken))

	s.Router.HandleFunc("GET /devices/{id}/current", s.RequireLogin(s.RequireDevice(s.handleCurrentApp)))
	s.Router.HandleFunc("GET /devices/{id}/installations/{iname}/preview", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleRenderConfigPreview))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/preview", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handlePushPreview))))

	// Firmware
	s.Router.HandleFunc("GET /devices/{id}/firmware", s.RequireLogin(s.RequireDevice(s.handleFirmwareGenerateGet)))
	s.Router.HandleFunc("GET /devices/{id}/firmware/release_notes", s.RequireLogin(s.RequireDevice(s.handleFirmwareReleaseNotes)))
	s.Router.HandleFunc("POST /devices/{id}/firmware", s.RequireLogin(s.RequireDevice(s.handleFirmwareGeneratePost)))
	s.Router.HandleFunc("POST /devices/{id}/ota", s.RequireLogin(s.RequireDevice(s.handleTriggerOTA))) // OTA Update

	s.Router.HandleFunc("GET /devices/{id}/update", s.RequireLogin(s.RequireDevice(s.handleUpdateDeviceGet)))
	s.Router.HandleFunc("POST /devices/{id}/update", s.RequireLogin(s.RequireDevice(s.handleUpdateDevicePost)))
	s.Router.HandleFunc("POST /devices/{id}/delete", s.RequireLogin(s.RequireDevice(s.handleDeleteDevice)))
	s.Router.HandleFunc("POST /devices/{id}/import_config", s.RequireLogin(s.RequireDevice(s.handleImportDeviceConfig)))
	s.Router.HandleFunc("POST /devices/{id}/reboot", s.RequireLogin(s.RequireDevice(s.handleRebootDevice)))

	s.Router.HandleFunc("POST /devices/{id}/update_firmware_settings", s.RequireLogin(s.RequireDevice(s.handleUpdateFirmwareSettings)))

	// Add this line for the TV View endpoint
	s.Router.HandleFunc("GET /devices/{id}/tv", s.RequireLogin(s.RequireDevice(s.handleDeviceTV)))

	// Websocket routes
	s.SetupWebsocketRoutes()

	// Health and Metrics
	s.Router.HandleFunc("GET /health", s.handleHealth)
	s.Router.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		promhttp.HandlerFor(s.PromGatherer, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})
	s.Router.HandleFunc("GET /dots", s.handleDots)

	if s.Config.EnablePprof {
		slog.Info("Enabling pprof", "path", "/debug/pprof/")
		s.Router.HandleFunc("GET /debug/pprof/", pprof.Index)
		s.Router.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
		s.Router.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
		s.Router.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
		s.Router.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Chain middlewares: Recover -> Metrics -> Gzip -> Logging -> Proxy -> Mux
	RecoverMiddleware(s.metricsMiddleware(GzipMiddleware(LoggingMiddleware(ProxyMiddleware(s.Router))))).ServeHTTP(w, r)
}

func (s *Server) GetTmpDir() string {
	return filepath.Join(s.DataDir, "tmp")
}

func (s *Server) handleDots(w http.ResponseWriter, r *http.Request) {
	widthStr := r.URL.Query().Get("w")
	heightStr := r.URL.Query().Get("h")
	radiusStr := r.URL.Query().Get("r")

	width := 64
	height := 32
	radius := 0.3

	if wVal, err := strconv.Atoi(widthStr); err == nil && wVal > 0 {
		width = wVal
	}
	if hVal, err := strconv.Atoi(heightStr); err == nil && hVal > 0 {
		height = hVal
	}
	if rVal, err := strconv.ParseFloat(radiusStr, 64); err == nil && rVal > 0 {
		radius = rVal
	}

	etag := fmt.Sprintf("\"%d-%d-%g\"", width, height, radius)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=31536000")

	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")

	rStr := strings.TrimPrefix(fmt.Sprintf("%g", radius), "0")
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" fill="#fff"><defs><pattern id="dot" width="1" height="1" patternUnits="userSpaceOnUse"><circle cx=".5" cy=".5" r="%s"/></pattern></defs><rect width="100%%" height="100%%" fill="url(#dot)"/></svg>`, width, height, rStr)

	if _, err := w.Write([]byte(svg)); err != nil {
		slog.Error("Failed to write dots SVG", "error", err)
	}
}

// GetMaxChunkSizeForDevice returns the chunk size budget based on device type.
func (s *Server) GetMaxChunkSizeForDevice(device *data.Device) int {
	if device == nil {
		if s.Config.MaxChunkSizeESP > 0 {
			return s.Config.MaxChunkSizeESP
		}
		return 450000
	}

	// 1. Raspberry Pi / Tronberry clients have expanded Linux memory capabilities (default: 5 MB).
	if device.Type == data.DeviceRaspberryPi ||
		device.Type == data.DeviceRaspberryPiWide ||
		device.Type == data.DeviceRaspberryPiSquare ||
		strings.EqualFold(device.Info.FirmwareType, "Tronberry") {
		if s.Config.MaxChunkSizeRPi > 0 {
			return s.Config.MaxChunkSizeRPi
		}
		return 5242880
	}

	// 2. Pixoticker has an ultra-compact memory buffer without PSRAM (50,000 bytes in firmware).
	if device.Type == data.DevicePixoticker {
		return 50000
	}

	// 3. All ESP devices (Tidbyt Gen 1, Gen 2, ESP32, S3 variants, MatrixPortal, Waveshare)
	// have PSRAM backing a ~460 KB firmware buffer (default: 450 KB).
	if s.Config.MaxChunkSizeESP > 0 {
		return s.Config.MaxChunkSizeESP
	}
	return 450000
}

// GetMaxChunkDurationForDevice returns the maximum duration in ms for a chunk.
// Returns 0 if unconstrained (split based on size only).
func (s *Server) GetMaxChunkDurationForDevice(device *data.Device) int {
	return 0
}

func (s *Server) invalidateAppChunks(deviceID string, iname string) {
	key := s.getAppChunksCacheKey(deviceID, iname)
	s.appChunksMu.Lock()
	delete(s.appChunksCache, key)
	s.appChunksMu.Unlock()
}

func (s *Server) cleanupAppChunks(deviceID string, app *data.App) {
	if app == nil {
		return
	}
	s.invalidateAppChunks(deviceID, app.Iname)
	deviceWebpDir, err := s.ensureDeviceImageDir(deviceID)
	if err != nil {
		return
	}
	appBasename := fmt.Sprintf("%s-%s", app.Name, app.Iname)
	chunkPrefix := fmt.Sprintf("%s_chunk_", appBasename)
	if entries, err := os.ReadDir(deviceWebpDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), chunkPrefix) && strings.HasSuffix(entry.Name(), ".webp") {
				_ = os.Remove(filepath.Join(deviceWebpDir, entry.Name()))
			}
		}
	}
	if p, err := securejoin.SecureJoin(deviceWebpDir, fmt.Sprintf("%s_chunks.json", appBasename)); err == nil {
		_ = os.Remove(p)
	}
	if p, err := securejoin.SecureJoin(deviceWebpDir, fmt.Sprintf("%s_full.webp", appBasename)); err == nil {
		_ = os.Remove(p)
	}
}

type chunkMeta struct {
	DurationMs int    `json:"duration_ms"`
	File       string `json:"file"`
}

func (s *Server) getAppChunksCacheKey(deviceID, iname string) string {
	return deviceID + ":" + iname
}

func (s *Server) getAppChunks(deviceID string, app *data.App) []chunker.RenderChunk {
	if app == nil {
		return nil
	}
	key := s.getAppChunksCacheKey(deviceID, app.Iname)
	s.appChunksMu.RLock()
	if cached, ok := s.appChunksCache[key]; ok && len(cached) > 0 {
		s.appChunksMu.RUnlock()
		return cached
	}
	s.appChunksMu.RUnlock()

	// Try loading from disk
	deviceWebpDir, err := s.ensureDeviceImageDir(deviceID)
	if err != nil {
		return nil
	}
	appBasename := fmt.Sprintf("%s-%s", app.Name, app.Iname)
	metaFile, err := securejoin.SecureJoin(deviceWebpDir, fmt.Sprintf("%s_chunks.json", appBasename))
	if err != nil {
		return nil
	}
	metaBytes, err := os.ReadFile(metaFile)
	if err == nil {
		var metas []chunkMeta
		if err := json.Unmarshal(metaBytes, &metas); err == nil && len(metas) > 0 {
			var chunks []chunker.RenderChunk
			for _, m := range metas {
				chunkFile := filepath.Base(m.File)
				chunkPath, err := securejoin.SecureJoin(deviceWebpDir, chunkFile)
				if err != nil {
					chunks = nil
					break
				}
				b, err := os.ReadFile(chunkPath)
				if err != nil {
					chunks = nil
					break
				}
				chunks = append(chunks, chunker.RenderChunk{
					WebPBytes:  b,
					DurationMs: m.DurationMs,
				})
			}
			if len(chunks) > 0 {
				s.appChunksMu.Lock()
				s.appChunksCache[key] = chunks
				s.appChunksMu.Unlock()
				return chunks
			}
		}
	}

	// Fallback: check if single .webp exists
	singlePath := s.getAppWebpPath(deviceWebpDir, app)
	if b, err := os.ReadFile(singlePath); err == nil && len(b) > 0 {
		durationMs := app.DisplayTime * 1000
		if durationMs <= 0 {
			durationMs = 15000
		}
		chunks := []chunker.RenderChunk{
			{
				WebPBytes:  b,
				DurationMs: durationMs,
			},
		}
		s.appChunksMu.Lock()
		s.appChunksCache[key] = chunks
		s.appChunksMu.Unlock()
		return chunks
	}

	return nil
}

func (s *Server) saveAppChunks(deviceID string, app *data.App, chunks []chunker.RenderChunk) error {
	if app == nil || len(chunks) == 0 {
		return nil
	}
	deviceWebpDir, err := s.ensureDeviceImageDir(deviceID)
	if err != nil {
		return err
	}
	appBasename := fmt.Sprintf("%s-%s", app.Name, app.Iname)

	// Remove old chunk files for this app to prevent orphaned chunks if chunk count changed
	chunkPrefix := fmt.Sprintf("%s_chunk_", appBasename)
	if entries, err := os.ReadDir(deviceWebpDir); err == nil {
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), chunkPrefix) && strings.HasSuffix(entry.Name(), ".webp") {
				_ = os.Remove(filepath.Join(deviceWebpDir, entry.Name()))
			}
		}
	}

	// The primary image (<appBasename>.webp) is what devices fetch on rotation or fallback.
	// It must always be safe for device hardware and stay within maxChunkSize, so we
	// write chunk 0 (the first chunk) to primaryPath.
	primaryPath := s.getAppWebpPath(deviceWebpDir, app)
	if err := os.MkdirAll(filepath.Dir(primaryPath), 0755); err != nil {
		return err
	}
	primaryBytes := chunks[0].WebPBytes
	if err := os.WriteFile(primaryPath, primaryBytes, 0644); err != nil {
		return err
	}

	// Save the complete unchunked animation (if available) to <appBasename>_full.webp
	// for rich web previews and dashboard display.
	if len(chunks[0].FullWebPBytes) > 0 {
		if fullPath, err := securejoin.SecureJoin(deviceWebpDir, fmt.Sprintf("%s_full.webp", appBasename)); err == nil {
			if err := os.WriteFile(fullPath, chunks[0].FullWebPBytes, 0644); err != nil {
				slog.Error("Failed to write full webp preview file", "path", fullPath, "error", err)
			}
		}
	}

	// If multiple chunks, write individual chunk files and chunk metadata JSON
	var metas []chunkMeta
	for i, chunk := range chunks {
		fileName := fmt.Sprintf("%s_chunk_%d.webp", appBasename, i)
		chunkPath, err := securejoin.SecureJoin(deviceWebpDir, fileName)
		if err != nil {
			slog.Error("Invalid chunk path", "file", fileName, "error", err)
			continue
		}
		if err := os.WriteFile(chunkPath, chunk.WebPBytes, 0644); err != nil {
			slog.Error("Failed to write chunk file", "path", chunkPath, "error", err)
		}
		metas = append(metas, chunkMeta{
			DurationMs: chunk.DurationMs,
			File:       fileName,
		})
	}

	metaFile, err := securejoin.SecureJoin(deviceWebpDir, fmt.Sprintf("%s_chunks.json", appBasename))
	if err == nil {
		if metaBytes, err := json.Marshal(metas); err == nil {
			_ = os.WriteFile(metaFile, metaBytes, 0644)
		}
	}

	// Store in memory cache (strip FullWebPBytes so large web preview payloads don't stay pinned in RAM)
	cachedChunks := make([]chunker.RenderChunk, len(chunks))
	for i, ch := range chunks {
		cachedChunks[i] = chunker.RenderChunk{
			WebPBytes:  ch.WebPBytes,
			DurationMs: ch.DurationMs,
		}
	}
	key := s.getAppChunksCacheKey(deviceID, app.Iname)
	s.appChunksMu.Lock()
	s.appChunksCache[key] = cachedChunks
	s.appChunksMu.Unlock()

	return nil
}
