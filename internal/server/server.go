package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/auth"
	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/gitutils"
	"tronbyt-server/internal/renderer"
	syncer "tronbyt-server/internal/sync"
	"tronbyt-server/internal/version"
	"tronbyt-server/web"

	"github.com/dustin/go-humanize"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gorilla/sessions"
	"github.com/gorilla/websocket"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/mod/semver"
	"golang.org/x/text/language"
	"gorm.io/gorm"

	"gopkg.in/yaml.v3"
)

// AppManifest reflects a subset of manifest.yaml for internal updates
type AppManifest struct {
	Broken       *bool   `yaml:"broken,omitempty"`
	BrokenReason *string `yaml:"brokenReason,omitempty"`
}

type Server struct {
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

	SystemAppsCache      []apps.AppMetadata
	SystemAppsCacheMutex sync.RWMutex

	UpdateAvailable  bool
	LatestReleaseURL string
}

type DeviceWithUIScale struct {
	Device            *data.Device
	BrightnessUI      int
	NightBrightnessUI int
}

// TemplateData is a struct to pass data to HTML templates.
type TemplateData struct {
	User                *data.User
	Users               []data.User // For admin view
	Config              *config.TemplateConfig
	Flashes             []string
	DevicesWithUIScales []DeviceWithUIScale
	Localizer           *i18n.Localizer // Pass Localizer directly

	UpdateAvailable  bool
	LatestReleaseURL string

	// Page-specific data
	Device            *data.Device
	SystemApps        []apps.AppMetadata
	CustomApps        []apps.AppMetadata
	DeviceTypeChoices map[string]string
	Form              CreateDeviceFormData

	// Repo Info for Admin/User Settings
	SystemRepoInfo      *gitutils.RepoInfo
	UserRepoInfo        *gitutils.RepoInfo
	GlobalSystemRepoURL string

	// App Config
	App       *data.App
	Schema    template.JS
	AppConfig map[string]any

	// Device Update Extras
	ColorFilterChoices map[string]string
	AvailableLocales   []string
	DefaultImgURL      string
	DefaultWsURL       string
	BrightnessUI       int
	NightBrightnessUI  int
	DimBrightnessUI    int

	// Firmware
	FirmwareBinsAvailable bool
	FirmwareVersion       string
	ServerVersion         string
	CommitHash            string
	IsAutoLoginActive     bool // Indicate if single-user auto-login is active
	UserCount             int  // Number of users, for registration logic
	DeleteOnCancel        bool // Indicate if app should be deleted on cancel
}

// CreateDeviceFormData represents the form data for creating a device.
type CreateDeviceFormData struct {
	Name           string
	DeviceType     string
	ImgURL         string
	WsURL          string
	APIKey         string
	Notes          string
	Brightness     int
	LocationSearch string
	LocationJSON   string
}

// Map template names to their file paths relative to web/templates
var templateFiles = map[string]string{
	"index":      "manager/index.html",
	"adminindex": "manager/adminindex.html",
	"login":      "auth/login.html",
	"register":   "auth/register.html",
	"edit":       "auth/edit.html",
	"create":     "manager/create.html",
	"addapp":     "manager/addapp.html",
	"configapp":  "manager/configapp.html",
	"uploadapp":  "manager/uploadapp.html",
	"firmware":   "manager/firmware.html",
	"update":     "manager/update.html",
}

func (s *Server) RefreshSystemAppsCache() {
	s.SystemAppsCacheMutex.Lock()
	defer s.SystemAppsCacheMutex.Unlock()

	slog.Info("Refreshing system apps cache")
	apps, err := apps.ListSystemApps(s.DataDir)
	if err == nil {
		s.SystemAppsCache = apps
		slog.Info("System apps cache refreshed", "count", len(s.SystemAppsCache))
	} else {
		slog.Error("Failed to refresh system apps cache", "error", err)
	}
}

func (s *Server) getSetting(key string) (string, error) {
	var setting data.Setting
	if err := s.DB.First(&setting, "key = ?", key).Error; err != nil {
		return "", err
	}
	return setting.Value, nil
}

func (s *Server) setSetting(key, value string) error {
	return s.DB.Save(&data.Setting{Key: key, Value: value}).Error
}

func NewServer(db *gorm.DB, cfg *config.Settings) *Server {
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
	}

	// Load Settings from DB
	// Secret Key
	secretKey, err := s.getSetting("secret_key")
	if err != nil || secretKey == "" {
		if cfg.SecretKey != "" && !strings.Contains(cfg.SecretKey, "insecure") {
			secretKey = cfg.SecretKey
		} else {
			b := make([]byte, 32)
			if _, err := rand.Read(b); err != nil {
				slog.Error("Failed to generate random secret key", "error", err)
				// Fallback to avoid crash, though this is critical
				secretKey = "insecure-fallback-key-" + fmt.Sprintf("%d", time.Now().UnixNano())
			} else {
				secretKey = base64.StdEncoding.EncodeToString(b)
			}
		}
		if err := s.setSetting("secret_key", secretKey); err != nil {
			slog.Error("Failed to save secret key to settings", "error", err)
		}
	}
	cfg.SecretKey = secretKey

	// System Repo
	repo, err := s.getSetting("system_apps_repo")
	if err == nil && repo != "" {
		cfg.SystemAppsRepo = repo
	}

	s.Store = sessions.NewCookieStore([]byte(cfg.SecretKey))

	// Configure Session Store
	s.Store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 30,
		HttpOnly: true,
		Secure:   cfg.Production == "1", // Set Secure flag in production
		SameSite: http.SameSiteLaxMode,
	}

	gob.Register(webauthn.SessionData{})

	// Load translations
	s.Bundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	// Load German translations
	i18nData, err := web.Assets.ReadFile("i18n/de.json")
	if err != nil {
		slog.Error("Failed to read German translation file", "error", err)
	} else {
		if _, err := s.Bundle.ParseMessageFileBytes(i18nData, "de.json"); err != nil {
			slog.Error("Failed to parse German translation file", "error", err)
		}
	}

	// Load English translations
	enI18nData, err := web.Assets.ReadFile("i18n/en.json")
	if err != nil {
		slog.Error("Failed to read English translation file", "error", err)
	} else {
		if _, err := s.Bundle.ParseMessageFileBytes(enI18nData, "en.json"); err != nil {
			slog.Error("Failed to parse English translation file", "error", err)
		}
	}

	funcMap := template.FuncMap{
		"seq": func(start, end int) []int {
			var s []int
			for i := start; i <= end; i++ {
				s = append(s, i)
			}
			return s
		},
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("dict expects even number of arguments")
			}
			dict := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
		"timeago": func(ts int64) string {
			if ts == 0 {
				return "never"
			}
			return humanize.Time(time.Unix(ts, 0))
		},
		"duration": func(d int64) string {
			dur := time.Duration(d)
			if dur.Seconds() < 60 {
				return fmt.Sprintf("%.3f s", dur.Seconds())
			}
			return dur.String()
		},
		"t": func(localizer *i18n.Localizer, messageID string, args ...any) string {
			localizeConfig := &i18n.LocalizeConfig{
				MessageID: messageID,
				DefaultMessage: &i18n.Message{
					ID:    messageID,
					Other: messageID,
				},
			}
			if len(args) > 0 {
				if num, ok := args[0].(int); ok {
					localizeConfig.PluralCount = num
				} else if dataMap, ok := args[0].(map[string]any); ok {
					localizeConfig.TemplateData = dataMap
				}
			}
			translated, err := localizer.Localize(localizeConfig)
			if err != nil {
				slog.Warn("Translation not found", "id", messageID, "error", err)
				return messageID // Fallback to message ID (which is the English string here)
			}
			return translated
		},
		"deref": func(s *string) string {
			if s == nil {
				return ""
			}
			return *s
		},
		"isPinned": func(device *data.Device, iname string) bool {
			if device.PinnedApp == nil {
				return false
			}
			return *device.PinnedApp == iname
		},
		"json": func(v any) (template.JS, error) {
			a, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return template.JS(a), nil
		},
		"string": func(v any) string {
			return fmt.Sprintf("%v", v)
		},
		"substr": func(s string, start, length int) string {
			if start < 0 {
				start = 0
			}
			if length < 0 {
				length = 0
			}
			end := start + length
			if end > len(s) {
				end = len(s)
			}
			if start > len(s) {
				return ""
			}
			return s[start:end]
		},
		"contains": func(slice []string, item string) bool {
			for _, s := range slice {
				if s == item {
					return true
				}
			}
			return false
		},
	}

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

	s.RefreshSystemAppsCache()

	go s.checkForUpdates()

	s.routes()
	return s
}

func (s *Server) routes() {
	// Conflict Resolution Handlers (Must be registered before conflicting wildcards?)
	// Actually order of registration doesn't matter in Go 1.22 for correctness, but presence matters.
	// But let's put them first for clarity.
	s.Router.HandleFunc("/static/ws", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })

	// Static files
	staticFS, err := fs.Sub(web.Assets, "static")
	if err != nil {
		slog.Error("Failed to sub static fs", "error", err)
	} else {
		fileServer := http.FileServer(http.FS(staticFS))
		// Register specific subdirectories to avoid conflict with /{id}/ws
		s.Router.Handle("/static/css/", http.StripPrefix("/static/", fileServer))
		s.Router.Handle("/static/js/", http.StripPrefix("/static/", fileServer))
		s.Router.Handle("/static/webfonts/", http.StripPrefix("/static/", fileServer))
		s.Router.Handle("/static/images/", http.StripPrefix("/static/", fileServer))
		s.Router.Handle("/static/favicon.ico", http.StripPrefix("/static/", fileServer))
	}

	// App Preview (Specific path)
	s.Router.HandleFunc("/preview/app/{id}", s.handleAppPreview)

	// API v0 Group - authenticated with Middleware
	s.Router.Handle("GET /v0/devices/{id}", s.APIAuthMiddleware(http.HandlerFunc(s.handleGetDevice)))
	s.Router.Handle("POST /v0/devices/{id}/push", s.APIAuthMiddleware(http.HandlerFunc(s.handlePush)))
	s.Router.Handle("GET /{id}/next", http.HandlerFunc(s.handleNextApp))
	s.Router.Handle("GET /v0/devices/{id}/installations", s.APIAuthMiddleware(http.HandlerFunc(s.handleListInstallations)))

	s.Router.Handle("PATCH /v0/devices/{id}", s.APIAuthMiddleware(http.HandlerFunc(s.handlePatchDevice)))
	s.Router.Handle("PATCH /v0/devices/{id}/installations/{iname}", s.APIAuthMiddleware(http.HandlerFunc(s.handlePatchInstallation)))
	s.Router.Handle("DELETE /v0/devices/{id}/installations/{iname}", s.APIAuthMiddleware(http.HandlerFunc(s.handleDeleteInstallationAPI)))

	s.Router.HandleFunc("GET /v0/dots", s.handleDots)

	// Web UI
	s.Router.HandleFunc("/", s.handleIndex)
	s.Router.HandleFunc("/adminindex", s.handleAdminIndex)
	s.Router.HandleFunc("POST /admin/{username}/deleteuser", s.handleDeleteUser)

	s.Router.HandleFunc("GET /auth/login", s.handleLoginGet)
	s.Router.HandleFunc("POST /auth/login", s.handleLoginPost)
	s.Router.HandleFunc("GET /auth/logout", s.handleLogout)
	s.Router.HandleFunc("GET /auth/register", s.handleRegisterGet)
	s.Router.HandleFunc("POST /auth/register", s.handleRegisterPost)
	s.Router.HandleFunc("GET /auth/edit", s.handleEditUserGet)
	s.Router.HandleFunc("POST /auth/edit", s.handleEditUserPost)
	s.Router.HandleFunc("POST /auth/generate_api_key", s.handleGenerateAPIKey)

	s.Router.HandleFunc("GET /devices/create", s.handleCreateDeviceGet)
	s.Router.HandleFunc("POST /devices/create", s.handleCreateDevicePost)

	s.Router.HandleFunc("GET /devices/{id}/addapp", s.handleAddAppGet)
	s.Router.HandleFunc("POST /devices/{id}/addapp", s.handleAddAppPost)

	s.Router.HandleFunc("POST /devices/{id}/{iname}/delete", s.handleDeleteApp)
	s.Router.HandleFunc("GET /devices/{id}/{iname}/config", s.handleConfigAppGet)
	s.Router.HandleFunc("POST /devices/{id}/{iname}/config", s.handleConfigAppPost)
	s.Router.HandleFunc("POST /devices/{id}/{iname}/schema_handler/{handler}", s.handleSchemaHandler)

	s.Router.HandleFunc("POST /devices/{id}/{iname}/toggle_pin", s.handleTogglePin)
	s.Router.HandleFunc("POST /devices/{id}/{iname}/toggle_enabled", s.handleToggleEnabled)
	s.Router.HandleFunc("POST /devices/{id}/{iname}/moveapp", s.handleMoveApp)
	s.Router.HandleFunc("POST /devices/{id}/{iname}/duplicate", s.handleDuplicateApp)
	s.Router.HandleFunc("POST /devices/{id}/reorder_apps", s.handleReorderApps)

	s.Router.HandleFunc("GET /devices/{id}/uploadapp", s.handleUploadAppGet)
	s.Router.HandleFunc("POST /devices/{id}/uploadapp", s.handleUploadAppPost)
	s.Router.HandleFunc("GET /devices/{id}/uploads/{filename}/delete", s.handleDeleteUpload)

	s.Router.HandleFunc("POST /set_api_key", s.handleSetAPIKey)
	s.Router.HandleFunc("POST /set_theme_preference", s.handleSetThemePreference)
	s.Router.HandleFunc("POST /set_user_repo", s.handleSetUserRepo)
	s.Router.HandleFunc("POST /refresh_user_repo", s.handleRefreshUserRepo)

	s.Router.HandleFunc("GET /export_user_config", s.handleExportUserConfig)
	s.Router.HandleFunc("POST /import_user_config", s.handleImportUserConfig)
	s.Router.HandleFunc("GET /devices/{id}/export_config", s.handleExportDeviceConfig)

	s.Router.HandleFunc("POST /set_system_repo", s.handleSetSystemRepo)
	s.Router.HandleFunc("POST /refresh_system_repo", s.handleRefreshSystemRepo)
	s.Router.HandleFunc("POST /update_firmware", s.handleUpdateFirmware)

	// App broken status (development only)
	s.Router.HandleFunc("POST /mark_app_broken", s.handleMarkAppBroken)
	s.Router.HandleFunc("POST /unmark_app_broken", s.handleUnmarkAppBroken)

	s.Router.HandleFunc("GET /devices/{id}/current", s.handleCurrentApp)
	s.Router.HandleFunc("GET /devices/{id}/installations/{iname}/preview", s.handleAppWebP)
	s.Router.HandleFunc("POST /devices/{id}/{iname}/preview", s.handlePreviewApp)

	// WebAuthn
	s.Router.HandleFunc("GET /auth/webauthn/register/begin", s.handleWebAuthnRegisterBegin)
	s.Router.HandleFunc("POST /auth/webauthn/register/finish", s.handleWebAuthnRegisterFinish)
	s.Router.HandleFunc("GET /auth/webauthn/login/begin", s.handleWebAuthnLoginBegin)
	s.Router.HandleFunc("POST /auth/webauthn/login/finish", s.handleWebAuthnLoginFinish)
	s.Router.HandleFunc("GET /auth/passkeys", s.handlePasskeysGet)

	// Firmware
	s.Router.HandleFunc("GET /devices/{id}/firmware", s.handleFirmwareGenerateGet)
	s.Router.HandleFunc("POST /devices/{id}/firmware", s.handleFirmwareGeneratePost)

	s.Router.HandleFunc("GET /devices/{id}/update", s.handleUpdateDeviceGet)
	s.Router.HandleFunc("POST /devices/{id}/update", s.handleUpdateDevicePost)
	s.Router.HandleFunc("POST /devices/{id}/delete", s.handleDeleteDevice)

	// Websocket catch-all (conflicts with many things, so registered last or handled carefully)
	s.Router.HandleFunc("/{id}/ws", s.handleWS)
	s.Router.HandleFunc("/ws", s.handleDashboardWS)
	s.Router.HandleFunc("/health", s.handleHealth)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		slog.Error("Failed to write health response", "error", err)
	}
}

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		slog.Debug("Request started", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
		slog.Debug("Request finished", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

func ProxyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			r.URL.Scheme = proto
		}
		if host := r.Header.Get("X-Forwarded-Host"); host != "" {
			r.Host = host
			r.URL.Host = host
		}
		next.ServeHTTP(w, r)
	})
}

func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("Panic recovered", "error", err, "stack", string(debug.Stack()))
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Chain middlewares: Recover -> Logging -> Proxy -> Mux
	RecoverMiddleware(LoggingMiddleware(ProxyMiddleware(s.Router))).ServeHTTP(w, r)
}

func (s *Server) renderTemplate(w http.ResponseWriter, r *http.Request, name string, tmplData TemplateData) {
	if tmplData.Config == nil {
		tmplData.Config = &config.TemplateConfig{
			EnableUserRegistration: s.Config.EnableUserRegistration,
			SingleUserAutoLogin:    s.Config.SingleUserAutoLogin,
		}
	}

	// Create localizer based on user's locale preference or request header
	if tmplData.Localizer == nil {
		tmplData.Localizer = s.getLocalizer(r)
	}

	// Set version
	tmplData.ServerVersion = version.Version
	tmplData.CommitHash = version.Commit

	// Set Update Info
	tmplData.UpdateAvailable = s.UpdateAvailable
	tmplData.LatestReleaseURL = s.LatestReleaseURL

	// Get User from session if not provided in tmplData
	session, _ := s.Store.Get(r, "session-name")
	if username, ok := session.Values["username"].(string); ok {
		var user data.User
		if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err == nil {
			tmplData.User = &user
		}
	}

	// Calculate IsAutoLoginActive
	var userCount int64
	if err := s.DB.Model(&data.User{}).Count(&userCount).Error; err != nil {
		slog.Error("Failed to count users for auto-login check", "error", err)
	} else {
		tmplData.IsAutoLoginActive = (s.Config.SingleUserAutoLogin == "1" && userCount == 1)
	}

	// Get and clear flash messages
	if flashes := session.Flashes(); len(flashes) > 0 {
		for _, f := range flashes {
			if msg, ok := f.(string); ok {
				tmplData.Flashes = append(tmplData.Flashes, msg)
			}
		}
	}

	if err := session.Save(r, w); err != nil {
		slog.Error("Failed to save session after flashing", "error", err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	// Verify template exists in map
	tmpl, ok := s.PageTemplates[name]
	if !ok {
		slog.Error("Template not found in map", "name", name)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Execute pre-parsed template
	err := tmpl.ExecuteTemplate(w, name, tmplData)
	if err != nil {
		slog.Error("Failed to render template", "template", name, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// localizeOrID helper to safely localize or return ID
func (s *Server) localizeOrID(localizer *i18n.Localizer, messageID string) string {
	config := &i18n.LocalizeConfig{
		MessageID: messageID,
		DefaultMessage: &i18n.Message{
			ID:    messageID,
			Other: messageID,
		},
	}
	translation, err := localizer.Localize(config)
	if err != nil {
		return messageID
	}
	return translation
}

func (s *Server) getLocalizer(r *http.Request) *i18n.Localizer {
	accept := r.Header.Get("Accept-Language")
	return i18n.NewLocalizer(s.Bundle, accept, language.English.String())
}

// getDeviceTypeChoices returns a map of device type values to display names
func (s *Server) getDeviceTypeChoices(localizer *i18n.Localizer) map[string]string {
	choices := make(map[string]string)
	// This should be dynamically generated from data.DeviceType enum

	choices[string(data.DeviceTidbytGen1)] = s.localizeOrID(localizer, "Tidbyt Gen1")
	choices[string(data.DeviceTidbytGen2)] = s.localizeOrID(localizer, "Tidbyt Gen2")
	choices[string(data.DevicePixoticker)] = s.localizeOrID(localizer, "Pixoticker")
	choices[string(data.DeviceRaspberryPi)] = s.localizeOrID(localizer, "Raspberry Pi")
	choices[string(data.DeviceRaspberryPiWide)] = s.localizeOrID(localizer, "Raspberry Pi Wide")
	choices[string(data.DeviceTronbytS3)] = s.localizeOrID(localizer, "Tronbyt S3")
	choices[string(data.DeviceTronbytS3Wide)] = s.localizeOrID(localizer, "Tronbyt S3 Wide")
	choices[string(data.DeviceMatrixPortal)] = s.localizeOrID(localizer, "MatrixPortal S3")
	choices[string(data.DeviceMatrixPortalWS)] = s.localizeOrID(localizer, "MatrixPortal S3 Waveshare")
	choices[string(data.DeviceOther)] = s.localizeOrID(localizer, "Other")
	return choices
}

// --- Handlers ---

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleIndex called")
	// Check auth
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		slog.Info("Not authenticated, redirecting to login")
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		slog.Error("User in session not found in DB", "username", username)
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var devicesWithUI []DeviceWithUIScale

	for i := range user.Devices {
		device := &user.Devices[i]
		slog.Debug("handleIndex device", "id", device.ID, "apps_count", len(device.Apps))

		// Sort Apps
		sort.Slice(device.Apps, func(i, j int) bool {
			return device.Apps[i].Order < device.Apps[j].Order
		})

		// Calculate UI Brightness
		bUI := device.Brightness.UIScale()

		devicesWithUI = append(devicesWithUI, DeviceWithUIScale{
			Device:       device,
			BrightnessUI: bUI,
		})
	}

	s.renderTemplate(w, r, "index", TemplateData{User: &user, DevicesWithUIScales: devicesWithUI})
}

func (s *Server) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok || username != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var users []data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").Find(&users).Error; err != nil {
		slog.Error("Failed to list users", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Sort Apps for each device
	for i := range users {
		for j := range users[i].Devices {
			dev := &users[i].Devices[j]
			sort.Slice(dev.Apps, func(a, b int) bool {
				return dev.Apps[a].Order < dev.Apps[b].Order
			})
		}
	}

	// We need to inject the current admin user into TemplateData for the header/nav
	var adminUser data.User
	for _, u := range users {
		if u.Username == "admin" {
			adminUser = u
			break
		}
	}

	s.renderTemplate(w, r, "adminindex", TemplateData{User: &adminUser, Users: users})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	targetUsername := r.PathValue("username")
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok || username != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if targetUsername == "admin" {
		http.Error(w, "Cannot delete admin", http.StatusBadRequest)
		return
	}

	var targetUser data.User
	if err := s.DB.Preload("Devices").First(&targetUser, "username = ?", targetUsername).Error; err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	// Clean up files
	for _, d := range targetUser.Devices {
		if err := os.RemoveAll(s.getDeviceWebPDir(d.ID)); err != nil {
			slog.Error("Failed to remove device webp directory", "device_id", d.ID, "error", err)
		}
	}
	userAppsDir := filepath.Join(s.DataDir, "users", targetUsername)
	if err := os.RemoveAll(userAppsDir); err != nil {
		slog.Error("Failed to remove user apps directory", "username", targetUsername, "error", err)
	}

	if err := s.DB.Delete(&targetUser).Error; err != nil {
		slog.Error("Failed to delete user", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/adminindex", http.StatusSeeOther)
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

	etag := fmt.Sprintf("\"%d-%d-%f\"", width, height, radius)
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "public, max-age=31536000")

	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "image/svg+xml")

	var sb strings.Builder
	sb.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	sb.WriteString(fmt.Sprintf("<svg xmlns=\"http://www.w3.org/2000/svg\" width=\"%d\" height=\"%d\" fill=\"#fff\">\n", width, height))

	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			sb.WriteString(fmt.Sprintf("<circle cx=\"%f\" cy=\"%f\" r=\"%f\"/>", float64(x)+0.5, float64(y)+0.5, radius))
		}
	}
	sb.WriteString("</svg>\n")

	if _, err := w.Write([]byte(sb.String())); err != nil {
		slog.Error("Failed to write dots SVG", "error", err)
	}
}

func (s *Server) getRealIP(r *http.Request) string {
	remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)
	if remoteIP == "" {
		remoteIP = r.RemoteAddr
	}

	trustedProxies := s.Config.TrustedProxies
	if trustedProxies == "" {
		return remoteIP
	}

	isTrusted := false
	if trustedProxies == "*" {
		isTrusted = true
	} else {
		// Simple check for comma-separated list of IPs
		proxies := strings.Split(trustedProxies, ",")
		for _, proxy := range proxies {
			if strings.TrimSpace(proxy) == remoteIP {
				isTrusted = true
				break
			}
		}
	}

	if isTrusted {
		xfwd := r.Header.Get("X-Forwarded-For")
		if xfwd != "" {
			parts := strings.Split(xfwd, ",")
			return strings.TrimSpace(parts[0])
		}
	}

	return remoteIP
}

func (s *Server) isTrustedNetwork(r *http.Request) bool {
	ipStr := s.getRealIP(r)

	if ipStr == "localhost" || ipStr == "::1" || ipStr == "127.0.0.1" {
		return true
	}

	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}

	return ip.IsLoopback() || ip.IsPrivate()
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleLoginGet called")

	// Check session
	session, _ := s.Store.Get(r, "session-name")
	if _, ok := session.Values["username"].(string); ok {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Auto-Login Check
	if s.Config.SingleUserAutoLogin == "1" {
		var count int64
		if err := s.DB.Model(&data.User{}).Count(&count).Error; err == nil && count == 1 {
			if s.isTrustedNetwork(r) {
				var user data.User
				s.DB.First(&user)
				session.Values["username"] = user.Username
				session.Options.MaxAge = 86400 * 30
				if err := session.Save(r, w); err != nil {
					slog.Error("Failed to save session for auto-login", "error", err)
				}
				slog.Info("Auto-logged in single user from trusted network", "username", user.Username, "ip", s.getRealIP(r))
				http.Redirect(w, r, "/", http.StatusSeeOther)
				return
			}
		}
	}

	var count int64
	if err := s.DB.Model(&data.User{}).Count(&count).Error; err != nil {
		slog.Error("Failed to count users", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if count == 0 {
		slog.Info("No users found, redirecting to registration for owner setup")
		http.Redirect(w, r, "/auth/register", http.StatusSeeOther)
		return
	}

	s.renderTemplate(w, r, "login", TemplateData{})
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleLoginPost called")
	username := r.FormValue("username")
	password := r.FormValue("password")

	var user data.User
	if err := s.DB.First(&user, "username = ?", username).Error; err != nil {
		slog.Warn("Login failed: user not found", "username", username)
		s.renderTemplate(w, r, "login", TemplateData{Flashes: []string{"Invalid username or password"}})
		return
	}

	valid, legacy, err := auth.VerifyPassword(user.Password, password)
	if err != nil {
		slog.Error("Password check error", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if !valid {
		slog.Warn("Login failed: invalid password", "username", username)
		s.renderTemplate(w, r, "login", TemplateData{Flashes: []string{"Invalid username or password"}})
		return
	}

	// Upgrade password if legacy
	if legacy {
		slog.Info("Upgrading password hash", "username", username)
		newHash, err := auth.HashPassword(password)
		if err == nil {
			s.DB.Model(&user).Update("password", newHash)
		} else {
			slog.Error("Failed to upgrade password hash", "error", err)
		}
	}

	// Login successful
	slog.Info("Login successful", "username", username)
	session, _ := s.Store.Get(r, "session-name")
	session.Values["username"] = user.Username

	if r.FormValue("remember") == "on" {
		session.Options.MaxAge = 86400 * 30
	} else {
		session.Options.MaxAge = 0
	}

	if err := session.Save(r, w); err != nil {
		slog.Error("Failed to save session", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	delete(session.Values, "username")
	if err := session.Save(r, w); err != nil {
		slog.Error("Failed to save session on logout", "error", err)
		// Non-fatal, redirect anyway
	}
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (s *Server) handleRegisterGet(w http.ResponseWriter, r *http.Request) {
	var count int64
	s.DB.Model(&data.User{}).Count(&count)

	if s.Config.EnableUserRegistration != "1" && count > 0 {
		session, _ := s.Store.Get(r, "session-name")
		currentUsername, ok := session.Values["username"].(string)
		if !ok || currentUsername != "admin" {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}
	}

	var flashes []string
	if count == 0 {
		localizer := s.getLocalizer(r)
		flashes = append(flashes, localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "System Setup: Please create the 'admin' user."}))
	}

	s.renderTemplate(w, r, "register", TemplateData{Flashes: flashes, UserCount: int(count)})
}

func (s *Server) handleRegisterPost(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")
	email := r.FormValue("email")

	var count int64
	s.DB.Model(&data.User{}).Count(&count)

	localizer := s.getLocalizer(r)

	if s.Config.EnableUserRegistration != "1" && count > 0 {
		session, _ := s.Store.Get(r, "session-name")
		currentUsername, ok := session.Values["username"].(string)
		if !ok || currentUsername != "admin" {
			http.Error(w, localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "User registration is not enabled."}), http.StatusForbidden)
			return
		}
	}

	// Special handling for the very first user (owner registration)
	if count == 0 {
		if username == "" {
			username = "admin"
		} else if username != "admin" {
			s.renderTemplate(w, r, "register", TemplateData{Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "System Setup: The first user must be 'admin'."})}})
			return
		}
	}

	if username == "" || password == "" {
		s.renderTemplate(w, r, "register", TemplateData{Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Username and password required"})}})
		return
	}

	// Check existing user only if count > 0 or username is explicitly provided for non-admin case
	if count > 0 || (count == 0 && username == "admin") {
		var existing data.User
		if err := s.DB.First(&existing, "username = ?", username).Error; err == nil {
			s.renderTemplate(w, r, "register", TemplateData{Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Username already exists"})}})
			return
		}
	}

	hashedPassword, err := auth.HashPassword(password)
	if err != nil {
		slog.Error("Failed to hash password", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	apiKey, _ := generateSecureToken(32)

	newUser := data.User{
		Username: username,
		Password: hashedPassword,
		Email:    email,
		APIKey:   apiKey,
	}

	if err := s.DB.Create(&newUser).Error; err != nil {
		slog.Error("Failed to create user", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Auto-login the first created user (admin)
	if count == 0 {
		session, _ := s.Store.Get(r, "session-name")
		session.Values["username"] = newUser.Username
		if err := session.Save(r, w); err != nil {
			slog.Error("Failed to save session for auto-login", "error", err)
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, "/adminindex", http.StatusSeeOther)
}

func (s *Server) getColorFilterChoices() map[string]string {
	return map[string]string{
		"None":          "None",
		"Dimmed":        "Dimmed",
		"Redshift":      "Redshift",
		"Warm":          "Warm",
		"Sunset":        "Sunset",
		"Sepia":         "Sepia",
		"Vintage":       "Vintage",
		"Dusk":          "Dusk",
		"Cool":          "Cool",
		"Black & White": "Black & White",
		"Ice":           "Ice",
		"Moonlight":     "Moonlight",
		"Neon":          "Neon",
		"Pastel":        "Pastel",
	}
}

func (s *Server) handleEditUserGet(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Credentials").First(&user, "username = ?", username).Error; err != nil {
		slog.Error("Failed to fetch user for edit", "username", username, "error", err)
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	// Get System Repo Info if admin (Stub for now or implement)	// Python: system_apps.get_system_repo_info
	// I'll leave it empty for now or implement later if critical.
	// Template expects 'system_repo_info' but I don't pass it in TemplateData explicitly,
	// unless I extend TemplateData or pass map.
	// Go TemplateData has User.
	// I need to add fields to TemplateData if I want to pass extra info.
	// 'FirmwareVersion' is there.

	firmwareVersion := "unknown"
	firmwareFile := filepath.Join(s.DataDir, "firmware", "firmware_version.txt")
	if bytes, err := os.ReadFile(firmwareFile); err == nil {
		firmwareVersion = strings.TrimSpace(string(bytes))
	}

	var systemRepoInfo *gitutils.RepoInfo
	if s.Config.SystemAppsRepo != "" {
		path := filepath.Join(s.DataDir, "system-apps")
		info, err := gitutils.GetRepoInfo(path, s.Config.SystemAppsRepo)
		if err != nil {
			slog.Error("Failed to get system repo info", "error", err)
		} else {
			systemRepoInfo = info
		}
	}

	var userRepoInfo *gitutils.RepoInfo
	if user.AppRepoURL != "" {
		path := filepath.Join(s.DataDir, "users", user.Username, "apps")
		info, err := gitutils.GetRepoInfo(path, user.AppRepoURL)
		if err != nil {
			slog.Error("Failed to get user repo info", "error", err)
		} else {
			userRepoInfo = info
		}
	}

	s.renderTemplate(w, r, "edit", TemplateData{
		User:                &user,
		FirmwareVersion:     firmwareVersion,
		SystemRepoInfo:      systemRepoInfo,
		UserRepoInfo:        userRepoInfo,
		GlobalSystemRepoURL: s.Config.SystemAppsRepo,
	})
}

func (s *Server) handleEditUserPost(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}

	oldPassword := r.FormValue("old_password")
	newPassword := r.FormValue("password")

	if oldPassword != "" && newPassword != "" {
		valid, _, err := auth.VerifyPassword(user.Password, oldPassword)
		if err != nil || !valid {
			s.renderTemplate(w, r, "edit", TemplateData{User: &user, Flashes: []string{"Invalid old password"}})
			return
		}

		hash, err := auth.HashPassword(newPassword)
		if err != nil {
			http.Error(w, "Failed to hash password", http.StatusInternalServerError)
			return
		}
		user.Password = hash
		if err := s.DB.Save(&user).Error; err != nil {
			slog.Error("Failed to update password", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		// Flash success?
	}

	http.Redirect(w, r, "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleGenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	apiKey, err := generateSecureToken(32)
	if err != nil {
		http.Error(w, "Failed to generate token", http.StatusInternalServerError)
		return
	}

	if err := s.DB.Model(&data.User{}).Where("username = ?", username).Update("api_key", apiKey).Error; err != nil {
		slog.Error("Failed to update API key", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleSetThemePreference(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	theme := r.FormValue("theme")
	if theme == "" {
		http.Error(w, "Theme required", http.StatusBadRequest)
		return
	}

	if err := s.DB.Model(&data.User{}).Where("username = ?", username).Update("theme_preference", theme).Error; err != nil {
		slog.Error("Failed to update theme preference", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

// generateSecureToken generates a URL-safe, base64 encoded, securely random string.
// This is used for generating API keys and device IDs.
func generateSecureToken(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b)[:length], nil // Take only requested length
}

func (s *Server) handleCreateDeviceGet(w http.ResponseWriter, r *http.Request) {
	// Check auth: only logged in users can create devices
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.First(&user, "username = ?", username).Error; err != nil {
		// User somehow disappeared from DB after session started
		slog.Error("User in session not found for create device GET", "username", username, "error", err)
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	localizer := s.getLocalizer(r)
	s.renderTemplate(w, r, "create", TemplateData{
		User:              &user,
		DeviceTypeChoices: s.getDeviceTypeChoices(localizer),
		Localizer:         localizer,
		Form:              CreateDeviceFormData{Brightness: 3}, // Default brightness
	})
}

func (s *Server) handleCreateDevicePost(w http.ResponseWriter, r *http.Request) {
	// Check auth
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		slog.Info("Not authenticated, redirecting to login")
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").First(&user, "username = ?", username).Error; err != nil {
		slog.Error("User in session not found for create device POST", "username", username, "error", err)
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	// Parse form data
	formData := CreateDeviceFormData{
		Name:           r.FormValue("name"),
		DeviceType:     r.FormValue("device_type"),
		ImgURL:         r.FormValue("img_url"),
		WsURL:          r.FormValue("ws_url"),
		Notes:          r.FormValue("notes"),
		LocationJSON:   r.FormValue("location"),
		LocationSearch: r.FormValue("location_search"), // Used for re-populating form
	}

	brightnessStr := r.FormValue("brightness")
	if brightness, err := strconv.Atoi(brightnessStr); err == nil {
		formData.Brightness = brightness
	} else {
		formData.Brightness = 3 // Default
	}

	// Validation
	localizer := s.getLocalizer(r)

	if formData.Name == "" {
		// Flash message
		slog.Warn("Validation error: Device name required")
		s.renderTemplate(w, r, "create", TemplateData{
			User:              &user,
			Flashes:           []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Unique name is required."}), localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Name is required."}), "Name is required."},
			DeviceTypeChoices: s.getDeviceTypeChoices(localizer),
			Localizer:         localizer,
			Form:              formData,
		})
		return
	}

	// Check if device name already exists for this user
	for _, dev := range user.Devices {
		if dev.Name == formData.Name {
			slog.Warn("Validation error: Device name already exists", "name", formData.Name)
			s.renderTemplate(w, r, "create", TemplateData{
				User:              &user,
				Flashes:           []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Unique name is required."}), localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "Name already exists."}), "Name already exists."}, // Added localized and plain text messages
				DeviceTypeChoices: s.getDeviceTypeChoices(localizer),
				Localizer:         localizer,
				Form:              formData,
			})
			return
		}
	}

	// Generate unique ID and API Key
	deviceID, err := generateSecureToken(8)
	if err != nil {
		slog.Error("Failed to generate device ID", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	apiKey, err := generateSecureToken(32)
	if err != nil {
		slog.Error("Failed to generate API key", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Parse location JSON
	var location data.DeviceLocation
	if formData.LocationJSON != "" && formData.LocationJSON != "{}" {
		var locMap map[string]any
		if err := json.Unmarshal([]byte(formData.LocationJSON), &locMap); err == nil {
			safeStr := func(v any) string {
				if s, ok := v.(string); ok {
					return s
				}
				if f, ok := v.(float64); ok {
					return fmt.Sprintf("%v", f)
				}
				return ""
			}
			location.Description = safeStr(locMap["description"])
			location.Lat = safeStr(locMap["lat"])
			location.Lng = safeStr(locMap["lng"])
			location.Locality = safeStr(locMap["locality"])
			location.PlaceID = safeStr(locMap["place_id"])
			location.Timezone = safeStr(locMap["timezone"])
		} else {
			slog.Warn("Invalid location JSON", "error", err)
		}
	}

	// Create new device
	newDevice := data.Device{
		ID:                    deviceID,
		Username:              user.Username,
		Name:                  formData.Name,
		Type:                  data.DeviceType(formData.DeviceType),
		APIKey:                apiKey,
		ImgURL:                formData.ImgURL, // Can be overridden by default logic later
		WsURL:                 formData.WsURL,  // Can be overridden by default logic later
		Notes:                 formData.Notes,
		Brightness:            data.Brightness(formData.Brightness),
		CustomBrightnessScale: "",
		NightBrightness:       0,
		DefaultInterval:       15,
		Location:              location,
		LastAppIndex:          0,
		InterstitialEnabled:   false,
	}

	// Set default ImgURL and WsURL if empty
	if newDevice.ImgURL == "" {
		newDevice.ImgURL = fmt.Sprintf("/%s/next", newDevice.ID)
	}
	// Need to determine absolute path for WS. For now, relative.
	if newDevice.WsURL == "" {
		newDevice.WsURL = fmt.Sprintf("/%s/ws", newDevice.ID)
	}

	// Save to DB
	if err := s.DB.Create(&newDevice).Error; err != nil {
		slog.Error("Failed to save new device to DB", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Create device webp directory
	deviceWebpDir := s.getDeviceWebPDir(newDevice.ID)
	if err := os.MkdirAll(deviceWebpDir, 0755); err != nil {
		slog.Error("Failed to create device webp directory", "path", deviceWebpDir, "error", err)
		// Not a fatal error, but log it
	}

	// Redirect to dashboard
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleGetDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Device ID required", http.StatusBadRequest)
		return
	}

	user, err := UserFromContext(r.Context())
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var device *data.Device

	if d, err := DeviceFromContext(r.Context()); err == nil {
		if d.ID != id {
			http.Error(w, "Forbidden: Device Key mismatch", http.StatusForbidden)
			return
		}
		device = d
	} else {
		for i := range user.Devices {
			if user.Devices[i].ID == id {
				device = &user.Devices[i]
				break
			}
		}
		if device == nil {
			http.Error(w, "Device not found", http.StatusNotFound)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.toDevicePayload(device)); err != nil {
		slog.Error("Failed to encode device JSON", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) handleListInstallations(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	user, _ := UserFromContext(r.Context())
	var device *data.Device

	if d, err := DeviceFromContext(r.Context()); err == nil {
		if d.ID != id {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		device = d
	} else {
		for i := range user.Devices {
			if user.Devices[i].ID == id {
				device = &user.Devices[i]
				break
			}
		}
	}

	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	response := map[string]any{
		"installations": device.Apps,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode installations JSON", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

type PushData struct {
	InstallationID    string `json:"installationID"`
	InstallationIDAlt string `json:"installationId"`
	Image             string `json:"image"`
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	user, _ := UserFromContext(r.Context())
	var device *data.Device
	if d, err := DeviceFromContext(r.Context()); err == nil {
		if d.ID != id {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		device = d
	} else {
		for i := range user.Devices {
			if user.Devices[i].ID == id {
				device = &user.Devices[i]
				break
			}
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	var dataReq PushData
	if err := json.NewDecoder(r.Body).Decode(&dataReq); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	installID := dataReq.InstallationID
	if installID == "" {
		installID = dataReq.InstallationIDAlt
	}

	imgBytes, err := base64.StdEncoding.DecodeString(dataReq.Image)
	if err != nil {
		http.Error(w, "Invalid Base64 Image", http.StatusBadRequest)
		return
	}

	if err := s.savePushedImage(device.ID, installID, imgBytes); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save image: %v", err), http.StatusInternalServerError)
		return
	}

	if installID != "" {
		if err := s.ensurePushedApp(device.ID, installID); err != nil {
			fmt.Printf("Error adding pushed app: %v\n", err)
		}
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("WebP received.")); err != nil {
		slog.Error("Failed to write WebP received message", "error", err)
		// Non-fatal, response already 200
	}
}

func (s *Server) savePushedImage(deviceID, installID string, data []byte) error {
	dir := filepath.Join(s.DataDir, "webp", deviceID, "pushed")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	var filename string
	if installID != "" {
		filename = installID + ".webp"
	} else {
		filename = fmt.Sprintf("__%d.webp", time.Now().UnixNano())
	}

	path := filepath.Join(dir, filename)
	return os.WriteFile(path, data, 0644)
}

func (s *Server) ensurePushedApp(deviceID, installID string) error {
	var count int64
	err := s.DB.Model(&data.App{}).Where("device_id = ? AND iname = ?", deviceID, installID).Count(&count).Error
	if err != nil {
		return err
	}

	if count > 0 {
		return nil
	}

	newApp := data.App{
		DeviceID:    deviceID,
		Iname:       installID,
		Name:        "pushed",
		UInterval:   10,
		DisplayTime: 0,
		Enabled:     true,
		Pushed:      true,
	}

	var maxOrder sql.NullInt64
	if err := s.DB.Model(&data.App{}).Where("device_id = ?", deviceID).Select("max(`order`)").Row().Scan(&maxOrder); err != nil {
		slog.Error("Failed to get max app order", "error", err)
		// Non-fatal, default to 0 for order (if maxOrder.Valid is false, maxOrder.Int64 is 0)
	}
	newApp.Order = int(maxOrder.Int64) + 1

	return s.DB.Create(&newApp).Error
}

func (s *Server) handleAddAppGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	s.SystemAppsCacheMutex.RLock()
	systemApps := make([]apps.AppMetadata, len(s.SystemAppsCache))
	copy(systemApps, s.SystemAppsCache)
	s.SystemAppsCacheMutex.RUnlock()

	customApps, err := apps.ListUserApps(s.DataDir, username)
	if err != nil {
		slog.Error("Failed to list user apps", "error", err)
		// Continue anyway
	}

	// Mark installed apps
	installedMap := make(map[string]bool)
	for _, da := range device.Apps {
		installedMap[da.Name] = true
	}

	for i := range systemApps {
		if installedMap[systemApps[i].ID] {
			systemApps[i].IsInstalled = true
		}
	}
	for i := range customApps {
		if installedMap[customApps[i].ID] {
			customApps[i].IsInstalled = true
		}
	}

	var systemRepoInfo *gitutils.RepoInfo
	if s.Config.SystemAppsRepo != "" {
		path := filepath.Join(s.DataDir, "system-apps")
		info, err := gitutils.GetRepoInfo(path, s.Config.SystemAppsRepo)
		if err != nil {
			slog.Error("Failed to get system repo info for addapp", "error", err)
		} else {
			systemRepoInfo = info
		}
	}

	s.renderTemplate(w, r, "addapp", TemplateData{
		User:           &user,
		Device:         device,
		SystemApps:     systemApps,
		CustomApps:     customApps,
		SystemRepoInfo: systemRepoInfo,
		Config:         &config.TemplateConfig{Production: s.Config.Production},
	})
}

func (s *Server) handleAddAppPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").First(&user, "username = ?", username).Error; err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	appName := r.FormValue("name")
	appPath := r.FormValue("path")
	notes := r.FormValue("notes")
	uintervalStr := r.FormValue("uinterval")
	displayTimeStr := r.FormValue("display_time")

	if appName == "" {
		http.Error(w, "App name required", http.StatusBadRequest)
		return
	}

	uinterval, _ := strconv.Atoi(uintervalStr)
	displayTime, _ := strconv.Atoi(displayTimeStr)

	// Construct source path
	realPath := filepath.Join(s.DataDir, appPath)
	// Safety check: ensure it's inside data dir
	absDataDir, _ := filepath.Abs(s.DataDir)
	absRealPath, _ := filepath.Abs(realPath)
	if len(absRealPath) < len(absDataDir) || absRealPath[:len(absDataDir)] != absDataDir {
		// Attempting traversal?
		// For now, just logging warning
		slog.Warn("Potential path traversal", "path", realPath)
	}

	// Generate iname
	safeName := ""
	for _, c := range appName {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			safeName += string(c)
		}
	}
	if len(safeName) > 20 {
		safeName = safeName[:20]
	}

	var iname string
	for i := 0; i < 100; i++ {
		suffix, _ := generateSecureToken(3)
		iname = fmt.Sprintf("%s-%s", safeName, suffix)

		var count int64
		if err := s.DB.Model(&data.App{}).Where("device_id = ? AND iname = ?", device.ID, iname).Count(&count).Error; err != nil {
			slog.Error("Failed to check iname uniqueness", "error", err)
			// Break and accept (DB create will fail if constrained, but we handle error there)
			break
		}
		if count == 0 {
			break
		}
		if i == 99 {
			// Fallback to timestamp
			iname = fmt.Sprintf("%s-%d", safeName, time.Now().Unix())
		}
	}

	installDir := filepath.Join(s.DataDir, "installations", iname)
	if err := os.MkdirAll(installDir, 0755); err != nil {
		slog.Error("Failed to create install dir", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Copy .star file
	destPath := filepath.Join(installDir, fmt.Sprintf("%s.star", iname)) // Rename to iname.star

	sourceFile, err := os.Open(realPath)
	if err != nil {
		slog.Error("Failed to open source file", "path", realPath, "error", err)
		http.Error(w, "App source not found", http.StatusBadRequest)
		return
	}
	defer func() {
		if err := sourceFile.Close(); err != nil {
			slog.Error("Failed to close source file", "error", err)
		}
	}()

	destFile, err := os.Create(destPath)
	if err != nil {
		slog.Error("Failed to create dest file", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := destFile.Close(); err != nil {
			slog.Error("Failed to close destination file", "error", err)
		}
	}()

	if _, err := io.Copy(destFile, sourceFile); err != nil {
		slog.Error("Failed to copy file", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	relPath := filepath.Join("installations", iname, fmt.Sprintf("%s.star", iname))

	// Create App in DB
	newApp := data.App{
		DeviceID:    device.ID,
		Iname:       iname,
		Name:        appName,
		UInterval:   uinterval,
		DisplayTime: displayTime,
		Notes:       notes,
		Enabled:     true,
		Path:        &relPath,
	}

	var maxOrder sql.NullInt64
	if err := s.DB.Model(&data.App{}).Where("device_id = ?", device.ID).Select("max(`order`)").Row().Scan(&maxOrder); err != nil {
		slog.Error("Failed to get max app order", "error", err)
		// Non-fatal, default to 0 for order
	}
	newApp.Order = int(maxOrder.Int64) + 1

	if err := s.DB.Create(&newApp).Error; err != nil {
		slog.Error("Failed to save app", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/devices/%s/%s/config?delete_on_cancel=true", device.ID, newApp.Iname), http.StatusSeeOther)
}

func (s *Server) handleAppPreview(w http.ResponseWriter, r *http.Request) {
	file := r.URL.Query().Get("file")
	if file == "" {
		http.Error(w, "File required", http.StatusBadRequest)
		return
	}

	// Security check
	if filepath.IsAbs(file) || filepath.Clean(file) == ".." || filepath.Clean(file) == "." {
		http.Error(w, "Invalid file path", http.StatusBadRequest)
		return
	}

	path := filepath.Join(s.DataDir, "system-apps", "apps", file)
	http.ServeFile(w, r, path)
}

func (s *Server) handleConfigAppGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iname := r.PathValue("iname")

	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	var app *data.App
	for i := range device.Apps {
		if device.Apps[i].Iname == iname {
			app = &device.Apps[i]
			break
		}
	}
	if app == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	// Get Schema
	var schemaBytes []byte
	if app.Path != nil && *app.Path != "" {
		appPath := s.resolveAppPath(*app.Path)
		script, err := os.ReadFile(appPath)
		if err == nil {
			schemaBytes, _ = renderer.GetSchema(script)
		}
	}
	if len(schemaBytes) == 0 {
		schemaBytes = []byte("{}")
	}

	deleteOnCancel := r.URL.Query().Get("delete_on_cancel") == "true"

	s.renderTemplate(w, r, "configapp", TemplateData{
		User:           &user,
		Device:         device,
		App:            app,
		Schema:         template.JS(schemaBytes),
		AppConfig:      app.Config,
		DeleteOnCancel: deleteOnCancel,
	})
}

func (s *Server) handleConfigAppPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iname := r.PathValue("iname")

	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	var app *data.App
	for i := range device.Apps {
		if device.Apps[i].Iname == iname {
			app = &device.Apps[i]
			break
		}
	}
	if app == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	// Parse JSON body
	var payload struct {
		Enabled             bool           `json:"enabled"`
		AutoPin             bool           `json:"autopin"`
		UInterval           int            `json:"uinterval"`
		DisplayTime         int            `json:"display_time"`
		Notes               string         `json:"notes"`
		Config              map[string]any `json:"config"`
		UseCustomRecurrence bool           `json:"use_custom_recurrence"`
		// ... schedule fields ...
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Update App
	app.Enabled = payload.Enabled
	app.AutoPin = payload.AutoPin
	app.UInterval = payload.UInterval
	app.DisplayTime = payload.DisplayTime
	app.Notes = payload.Notes
	app.Config = payload.Config

	// Save to DB
	if err := s.DB.Save(app).Error; err != nil {
		slog.Error("Failed to save app config", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Trigger Render (async)
	// For now, reset last render to force update next check
	s.DB.Model(app).Update("last_render", 0)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleSchemaHandler(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iname := r.PathValue("iname")
	handler := r.PathValue("handler")

	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	var app *data.App
	for i := range device.Apps {
		if device.Apps[i].Iname == iname {
			app = &device.Apps[i]
			break
		}
	}
	if app == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	// Parse Body
	var payload struct {
		Param  string         `json:"param"`
		Config map[string]any `json:"config"`
	}

	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Convert Config to map[string]string for Pixlet
	configStr := make(map[string]string)
	for k, v := range payload.Config {
		configStr[k] = fmt.Sprintf("%v", v)
	}

	// Read Script
	if app.Path == nil || *app.Path == "" {
		http.Error(w, "App path not set", http.StatusBadRequest)
		return
	}
	appPath := s.resolveAppPath(*app.Path)
	script, err := os.ReadFile(appPath)
	if err != nil {
		slog.Error("Failed to read app script", "path", appPath, "error", err)
		http.Error(w, "Failed to read app script", http.StatusInternalServerError)
		return
	}

	// Call Handler
	result, err := renderer.CallSchemaHandler(r.Context(), script, configStr, handler, payload.Param)
	if err != nil {
		slog.Error("Schema handler failed", "handler", handler, "error", err)
		http.Error(w, "Schema handler failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write([]byte(result)); err != nil {
		slog.Error("Failed to write schema handler response", "error", err)
	}
}

func (s *Server) handleCurrentApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Auth check? Device card is in dashboard, so user is logged in.
	// But image might be loaded by browser. Session cookie works.
	session, _ := s.Store.Get(r, "session-name")
	if _, ok := session.Values["username"].(string); !ok {
		// Try API Key? No, dashboard use.
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var device data.Device
	if err := s.DB.Preload("Apps").First(&device, "id = ?", id).Error; err != nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	// Logic to get current app image without rotating
	// Reuse determineNextApp but we need to know "current".
	// determineNextApp calculates next based on LastAppIndex.
	// If we want "current", we probably want the one at LastAppIndex?
	// Or maybe just re-run rotation logic without side effects?
	// Python uses _get_app_to_display(advance_index=False).

	// For simplicity, let's just serve the WebP of the app at LastAppIndex if valid.
	// Or call determineNextApp but don't save state.
	// But determineNextApp *finds* the next app.
	// If LastAppIndex points to the *just displayed* app, then "current" is that one.

	// HACK: Just send default image if complex.
	// Better: Implement `GetCurrentAppImage` in rotation.go.
	imgData, _, err := s.GetCurrentAppImage(r.Context(), &device)
	if err != nil {
		s.sendDefaultImage(w, r, &device)
		return
	}

	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "no-cache")
	if _, err := w.Write(imgData); err != nil {
		slog.Error("Failed to write current app image", "error", err)
	}
}

func (s *Server) handleAppWebP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iname := r.PathValue("iname")

	// Auth check
	session, _ := s.Store.Get(r, "session-name")
	if _, ok := session.Values["username"].(string); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Verify device exists (optional but good)
	var device data.Device
	if err := s.DB.First(&device, "id = ?", id).Error; err != nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	// Find app (optional, mostly to check if it exists)
	// We can just look for file.
	// <name>-<iname>.webp. We need name.
	var app data.App
	if err := s.DB.Where("device_id = ? AND iname = ?", id, iname).First(&app).Error; err != nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	webpDir := s.getDeviceWebPDir(id)
	filename := fmt.Sprintf("%s-%s.webp", app.Name, app.Iname)
	path := filepath.Join(webpDir, filename)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Try pushed
		path = filepath.Join(webpDir, "pushed", iname+".webp")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			s.sendDefaultImage(w, r, &device)
			return
		}
	}

	http.ServeFile(w, r, path)
}

func (s *Server) handlePreviewApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iname := r.PathValue("iname")

	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	var app *data.App
	for i := range device.Apps {
		if device.Apps[i].Iname == iname {
			app = &device.Apps[i]
			break
		}
	}
	if app == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	// Parse Config from Body (optional overrides)
	// JS: previewApp(..., config) sends JSON.
	var configOverride map[string]any
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&configOverride)
	}

	// Merge config
	config := make(map[string]string)
	// Load saved config first
	for k, v := range app.Config {
		config[k] = fmt.Sprintf("%v", v)
	}
	// Apply overrides
	for k, v := range configOverride {
		config[k] = fmt.Sprintf("%v", v)
	}

	// Render
	if app.Path == nil || *app.Path == "" {
		http.Error(w, "App path not set", http.StatusBadRequest)
		return
	}
	appPath := s.resolveAppPath(*app.Path)
	script, err := os.ReadFile(appPath)
	if err != nil {
		http.Error(w, "Failed to read app script", http.StatusInternalServerError)
		return
	}

	imgBytes, err := renderer.Render(r.Context(), script, config, 64, 32)
	if err != nil {
		slog.Error("Preview render failed", "error", err)
		http.Error(w, "Render failed", http.StatusInternalServerError)
		return
	}

	// Push preview image to device (ephemeral)
	// Use handlePush logic or direct savePushedImage
	// The JS expects "OK" and side-effect is push.
	// Wait, JS previewApp does NOT expect image in response?
	// JS: if (response.ok) ... console.log('Preview request failed'...)
	// Python: await push_image(...) and returns 200.

	// Use __preview_<timestamp>.webp or similar?
	// Or just push directly.
	// Use savePushedImage with installID (overwrite pushed image)
	if err := s.savePushedImage(device.ID, app.Iname, imgBytes); err != nil {
		http.Error(w, "Failed to push preview", http.StatusInternalServerError)
		return
	}

	// Notify device via Websocket (Broadcaster)
	s.Broadcaster.Notify(device.ID)

	w.WriteHeader(http.StatusOK)
}

// API Handlers

type DeviceUpdate struct {
	Brightness          *int    `json:"brightness"`
	IntervalSec         *int    `json:"intervalSec"`
	NightModeEnabled    *bool   `json:"nightModeEnabled"`
	NightModeApp        *string `json:"nightModeApp"`
	NightModeBrightness *int    `json:"nightModeBrightness"`
	NightModeStartTime  *string `json:"nightModeStartTime"`
	NightModeEndTime    *string `json:"nightModeEndTime"`
	DimModeStartTime    *string `json:"dimModeStartTime"`
	DimModeBrightness   *int    `json:"dimModeBrightness"`
	PinnedApp           *string `json:"pinnedApp"`
	AutoDim             *bool   `json:"autoDim"` // Legacy
}

type DevicePayload struct {
	ID           string          `json:"id"`
	Type         data.DeviceType `json:"type"`
	DisplayName  string          `json:"displayName"`
	Notes        string          `json:"notes"`
	IntervalSec  int             `json:"intervalSec"`
	Brightness   int             `json:"brightness"`
	NightMode    NightMode       `json:"nightMode"`
	DimMode      DimMode         `json:"dimMode"`
	PinnedApp    *string         `json:"pinnedApp"`
	Interstitial Interstitial    `json:"interstitial"`
	LastSeen     *string         `json:"lastSeen"`
	Info         DeviceInfo      `json:"info"`
	AutoDim      bool            `json:"autoDim"`
}

type NightMode struct {
	Enabled    bool   `json:"enabled"`
	App        string `json:"app"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
	Brightness int    `json:"brightness"`
}

type DimMode struct {
	StartTime  *string `json:"startTime"`
	Brightness *int    `json:"brightness"`
}

type Interstitial struct {
	Enabled bool    `json:"enabled"`
	App     *string `json:"app"`
}

type DeviceInfo struct {
	FirmwareVersion string `json:"firmwareVersion"`
	FirmwareType    string `json:"firmwareType"`
	ProtocolVersion *int   `json:"protocolVersion"`
	MACAddress      string `json:"macAddress"`
	ProtocolType    string `json:"protocolType"`
}

func (s *Server) toDevicePayload(d *data.Device) DevicePayload {
	info := DeviceInfo{
		FirmwareVersion: d.Info.FirmwareVersion,
		FirmwareType:    d.Info.FirmwareType,
		ProtocolVersion: d.Info.ProtocolVersion,
		MACAddress:      d.Info.MACAddress,
		ProtocolType:    d.Info.ProtocolType,
	}

	var lastSeen *string
	if d.LastSeen != nil {
		iso := d.LastSeen.Format(time.RFC3339)
		lastSeen = &iso
	}

	var dimBrightnessPtr *int
	if d.DimBrightness != nil {
		val := int(*d.DimBrightness)
		dimBrightnessPtr = &val
	}

	return DevicePayload{
		ID:          d.ID,
		Type:        d.Type,
		DisplayName: d.Name,
		Notes:       d.Notes,
		IntervalSec: d.DefaultInterval,
		Brightness:  int(d.Brightness),
		NightMode: NightMode{
			Enabled:    d.NightModeEnabled,
			App:        d.NightModeApp,
			StartTime:  d.NightStart,
			EndTime:    d.NightEnd,
			Brightness: int(d.NightBrightness),
		},
		DimMode: DimMode{
			StartTime:  d.DimTime,
			Brightness: dimBrightnessPtr,
		},
		PinnedApp: d.PinnedApp,
		Interstitial: Interstitial{
			Enabled: d.InterstitialEnabled,
			App:     d.InterstitialApp,
		},
		LastSeen: lastSeen,
		Info:     info,
		AutoDim:  d.NightModeEnabled,
	}
}

func (s *Server) handlePatchDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Auth handled by middleware, get device
	var device *data.Device
	if d, err := DeviceFromContext(r.Context()); err == nil {
		if d.ID != id {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		device = d
	} else if u, err := UserFromContext(r.Context()); err == nil {
		for i := range u.Devices {
			if u.Devices[i].ID == id {
				device = &u.Devices[i]
				break
			}
		}
	}

	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	var update DeviceUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if update.Brightness != nil {
		device.Brightness = data.Brightness(*update.Brightness)
	}
	if update.IntervalSec != nil {
		device.DefaultInterval = *update.IntervalSec
	}
	if update.NightModeEnabled != nil {
		device.NightModeEnabled = *update.NightModeEnabled
	}
	if update.AutoDim != nil {
		device.NightModeEnabled = *update.AutoDim
	}
	if update.NightModeApp != nil {
		device.NightModeApp = *update.NightModeApp
	}
	if update.NightModeBrightness != nil {
		device.NightBrightness = data.Brightness(*update.NightModeBrightness)
	}
	if update.PinnedApp != nil {
		if *update.PinnedApp == "" {
			device.PinnedApp = nil
		} else {
			device.PinnedApp = update.PinnedApp
		}
	}

	if update.NightModeStartTime != nil {
		device.NightStart = *update.NightModeStartTime
	}
	if update.NightModeEndTime != nil {
		device.NightEnd = *update.NightModeEndTime
	}
	if update.DimModeStartTime != nil {
		device.DimTime = update.DimModeStartTime
	}
	if update.DimModeBrightness != nil {
		val := data.Brightness(*update.DimModeBrightness)
		device.DimBrightness = &val
	}

	if err := s.DB.Save(device).Error; err != nil {
		http.Error(w, "Failed to update device", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.toDevicePayload(device)); err != nil {
		slog.Error("Failed to encode device", "error", err)
	}
}

type InstallationUpdate struct {
	Enabled           *bool `json:"enabled"`
	Pinned            *bool `json:"pinned"`
	RenderIntervalMin *int  `json:"renderIntervalMin"`
	DisplayTimeSec    *int  `json:"displayTimeSec"`
}

func (s *Server) handlePatchInstallation(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	iname := r.PathValue("iname")

	// Auth & Device fetch (same as above)
	// ... (Simplification: Assuming middleware checks API key for device or user session)
	// Actually middleware puts user/device in context.

	var device *data.Device
	// ... fetch device ...
	if d, err := DeviceFromContext(r.Context()); err == nil {
		if d.ID != deviceID {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		device = d
	} else if u, err := UserFromContext(r.Context()); err == nil {
		for i := range u.Devices {
			if u.Devices[i].ID == deviceID {
				device = &u.Devices[i]
				break
			}
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	var app *data.App
	for i := range device.Apps {
		if device.Apps[i].Iname == iname {
			app = &device.Apps[i]
			break
		}
	}
	if app == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	var update InstallationUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if update.Enabled != nil {
		app.Enabled = *update.Enabled
	}
	if update.RenderIntervalMin != nil {
		app.UInterval = *update.RenderIntervalMin
	}
	if update.DisplayTimeSec != nil {
		app.DisplayTime = *update.DisplayTimeSec
	}
	if update.Pinned != nil {
		if *update.Pinned {
			device.PinnedApp = &app.Iname
		} else if device.PinnedApp != nil && *device.PinnedApp == app.Iname {
			device.PinnedApp = nil
		}
		// Save device for pinned change
		s.DB.Save(device)
	}

	if err := s.DB.Save(app).Error; err != nil {
		http.Error(w, "Failed to update app", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(app); err != nil {
		slog.Error("Failed to encode app", "error", err)
	}
}

func (s *Server) handleDeleteInstallationAPI(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	iname := r.PathValue("iname")

	// Auth & Device fetch
	var device *data.Device
	if d, err := DeviceFromContext(r.Context()); err == nil {
		if d.ID != deviceID {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		device = d
	} else if u, err := UserFromContext(r.Context()); err == nil {
		for i := range u.Devices {
			if u.Devices[i].ID == deviceID {
				device = &u.Devices[i]
				break
			}
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	if err := s.DB.Where("device_id = ? AND iname = ?", device.ID, iname).Delete(&data.App{}).Error; err != nil {
		http.Error(w, "Failed to delete app", http.StatusInternalServerError)
		return
	}

	// Clean up files (install dir and webp)
	installDir := filepath.Join(s.DataDir, "installations", iname)
	if err := os.RemoveAll(installDir); err != nil {
		slog.Error("Failed to remove install directory", "path", installDir, "error", err)
	}

	webpDir := s.getDeviceWebPDir(device.ID)
	matches, _ := filepath.Glob(filepath.Join(webpDir, fmt.Sprintf("*-%s.webp", iname)))
	for _, match := range matches {
		if err := os.Remove(match); err != nil {
			slog.Error("Failed to remove webp file", "path", match, "error", err)
		}
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("App deleted.")); err != nil {
		slog.Error("Failed to write response", "error", err)
	}
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iname := r.PathValue("iname")

	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	// Delete App
	if err := s.DB.Where("device_id = ? AND iname = ?", device.ID, iname).Delete(&data.App{}).Error; err != nil {
		slog.Error("Failed to delete app", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Clean up files?
	// installations/<iname>
	installDir := filepath.Join(s.DataDir, "installations", iname)
	if err := os.RemoveAll(installDir); err != nil {
		slog.Error("Failed to remove install directory", "path", installDir, "error", err)
	}

	// Clean up webp
	webpDir := s.getDeviceWebPDir(device.ID)
	// Need app name to find webp: <name>-<iname>.webp
	// Since we already deleted the app from DB, we might not have the name easily unless we queried before delete.
	// But we can glob: *<iname>.webp
	matches, _ := filepath.Glob(filepath.Join(webpDir, fmt.Sprintf("*-%s.webp", iname)))
	for _, match := range matches {
		if err := os.Remove(match); err != nil {
			slog.Error("Failed to remove app webp file", "path", match, "error", err)
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTogglePin(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iname := r.PathValue("iname")

	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	// Logic
	newPinned := iname
	if device.PinnedApp != nil && *device.PinnedApp == iname {
		newPinned = ""
	}

	if newPinned == "" {
		if err := s.DB.Model(device).Update("pinned_app", nil).Error; err != nil {
			slog.Error("Failed to unpin app", "error", err)
		}
	} else {
		if err := s.DB.Model(device).Update("pinned_app", newPinned).Error; err != nil {
			slog.Error("Failed to pin app", "error", err)
		}
	}

	// Notify Dashboard
	s.Broadcaster.Notify("user:" + username)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleToggleEnabled(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iname := r.PathValue("iname")

	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	var app *data.App
	for i := range device.Apps {
		if device.Apps[i].Iname == iname {
			app = &device.Apps[i]
			break
		}
	}
	if app == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	app.Enabled = !app.Enabled
	if err := s.DB.Model(app).Update("enabled", app.Enabled).Error; err != nil {
		slog.Error("Failed to toggle enabled", "error", err)
	}

	// Notify Dashboard
	s.Broadcaster.Notify("user:" + username)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleMoveApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iname := r.PathValue("iname")
	direction := r.FormValue("direction")

	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	// Sort
	appsList := make([]data.App, len(device.Apps))
	copy(appsList, device.Apps)
	sort.Slice(appsList, func(i, j int) bool {
		return appsList[i].Order < appsList[j].Order
	})

	idx := -1
	for i, app := range appsList {
		if app.Iname == iname {
			idx = i
			break
		}
	}

	if idx == -1 {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	switch direction {
	case "up":
		if idx > 0 {
			appsList[idx], appsList[idx-1] = appsList[idx-1], appsList[idx]
		}
	case "down":
		if idx < len(appsList)-1 {
			appsList[idx], appsList[idx+1] = appsList[idx+1], appsList[idx]
		}
	case "top":
		if idx > 0 {
			app := appsList[idx]
			// Shift others down
			copy(appsList[1:idx+1], appsList[0:idx])
			appsList[0] = app
		}
	case "bottom":
		if idx < len(appsList)-1 {
			app := appsList[idx]
			// Shift others up
			copy(appsList[idx:len(appsList)-1], appsList[idx+1:])
			appsList[len(appsList)-1] = app
		}
	}

	// Save new order
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		for i := range appsList {
			if appsList[i].Order != i {
				if err := tx.Model(&appsList[i]).Update("order", i).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("Failed to update app order", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Notify Dashboard
	s.Broadcaster.Notify("user:" + username)

	w.WriteHeader(http.StatusOK)
}
func (s *Server) handleDuplicateApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iname := r.PathValue("iname")

	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	var originalApp *data.App
	for i := range device.Apps {
		if device.Apps[i].Iname == iname {
			originalApp = &device.Apps[i]
			break
		}
	}
	if originalApp == nil {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	// Generate new iname
	safeName := ""
	for _, c := range originalApp.Name {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			safeName += string(c)
		}
	}
	if len(safeName) > 20 {
		safeName = safeName[:20]
	}

	var newIname string
	for i := 0; i < 100; i++ {
		suffix, _ := generateSecureToken(3)
		newIname = fmt.Sprintf("%s-%s", safeName, suffix)

		var count int64
		if err := s.DB.Model(&data.App{}).Where("device_id = ? AND iname = ?", device.ID, newIname).Count(&count).Error; err != nil {
			break
		}
		if count == 0 {
			break
		}
	}

	// Copy App
	newApp := *originalApp
	newApp.ID = 0 // GORM will generate new ID
	newApp.Iname = newIname
	newApp.LastRender = 0
	newApp.Order = originalApp.Order + 1
	newApp.Pushed = false

	// Handle File Copy for User Apps
	if originalApp.Path != nil {
		origPath := *originalApp.Path
		if !strings.HasPrefix(origPath, "system-apps/") {
			// User App - Deep Copy
			installDir := filepath.Join(s.DataDir, "installations", newIname)
			if err := os.MkdirAll(installDir, 0755); err != nil {
				slog.Error("Failed to create install dir for duplicate", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			srcPath := s.resolveAppPath(origPath)
			destFilename := fmt.Sprintf("%s.star", newIname)
			destPath := filepath.Join(installDir, destFilename)

			src, err := os.Open(srcPath)
			if err != nil {
				slog.Error("Failed to open source app for duplication", "path", srcPath, "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			defer func() {
				if err := src.Close(); err != nil {
					slog.Error("Failed to close source file", "error", err)
				}
			}()

			dst, err := os.Create(destPath)
			if err != nil {
				slog.Error("Failed to create dest app for duplication", "path", destPath, "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
			defer func() {
				if err := dst.Close(); err != nil {
					slog.Error("Failed to close dest file", "error", err)
				}
			}()

			if _, err := io.Copy(dst, src); err != nil {
				slog.Error("Failed to copy app content", "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			relPath := filepath.Join("installations", newIname, destFilename)
			newApp.Path = &relPath
		}
	}

	// Transaction for reordering and creating
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		// Shift orders
		for i := range device.Apps {
			if device.Apps[i].Order > originalApp.Order {
				if err := tx.Model(&device.Apps[i]).Update("order", device.Apps[i].Order+1).Error; err != nil {
					return err
				}
			}
		}

		if err := tx.Create(&newApp).Error; err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		slog.Error("Failed to duplicate app", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Notify Dashboard
	s.Broadcaster.Notify("user:" + username)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleReorderApps(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	draggedIname := r.FormValue("dragged_iname")
	targetIname := r.FormValue("target_iname")
	insertAfter := r.FormValue("insert_after") == "true"

	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	appsList := make([]data.App, len(device.Apps))
	copy(appsList, device.Apps)
	sort.Slice(appsList, func(i, j int) bool {
		return appsList[i].Order < appsList[j].Order
	})

	draggedIdx := -1
	targetIdx := -1

	for i, app := range appsList {
		if app.Iname == draggedIname {
			draggedIdx = i
		}
		if app.Iname == targetIname {
			targetIdx = i
		}
	}

	if draggedIdx == -1 || targetIdx == -1 {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	if draggedIdx == targetIdx {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Move
	// Calculate new index for dragged item
	newIdx := targetIdx
	if insertAfter {
		newIdx++
	}
	// Adjust if we are moving downwards
	if draggedIdx < newIdx {
		newIdx--
	}

	// Remove dragged
	draggedApp := appsList[draggedIdx]
	appsList = append(appsList[:draggedIdx], appsList[draggedIdx+1:]...)

	// Insert at newIdx
	if newIdx >= len(appsList) {
		appsList = append(appsList, draggedApp)
	} else {
		appsList = append(appsList[:newIdx+1], appsList[newIdx:]...)
		appsList[newIdx] = draggedApp
	}

	// Save new order
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		for i := range appsList {
			if appsList[i].Order != i {
				if err := tx.Model(&appsList[i]).Update("order", i).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("Failed to update app order", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Notify Dashboard
	s.Broadcaster.Notify("user:" + username)

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDeleteUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	filename := r.PathValue("filename")

	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	// Check if file is in use
	inUse := false
	for _, dev := range user.Devices {
		for _, app := range dev.Apps {
			if app.Path != nil && filepath.Base(*app.Path) == filename {
				inUse = true
				break
			}
		}
		if inUse {
			break
		}
	}

	if inUse {
		// Flash message? Go templates need flash support.
		// For now just error or redirect.
		slog.Warn("Cannot delete upload in use", "filename", filename)
		http.Redirect(w, r, fmt.Sprintf("/devices/%s/addapp", id), http.StatusSeeOther)
		return
	}

	// Delete
	userAppsPath := filepath.Join(s.DataDir, "users", username, "apps")
	// Find directory with this filename (it's inside a subdir usually: apps/<appname>/<filename>)
	// Or is it flattened?
	// handleUploadAppPost: appDir := .../apps/<appName>; dstPath := .../<filename>
	// filename is e.g. "my_app.star". App name is "my_app".
	// So path is users/<user>/apps/<app_name>/<filename>.

	appName := strings.TrimSuffix(filename, filepath.Ext(filename))
	appDir := filepath.Join(userAppsPath, appName)

	// Security check
	if !strings.HasPrefix(appDir, userAppsPath) {
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	if err := os.RemoveAll(appDir); err != nil {
		slog.Error("Failed to remove app upload dir", "path", appDir, "error", err)
	}

	http.Redirect(w, r, fmt.Sprintf("/devices/%s/addapp", id), http.StatusSeeOther)
}

func (s *Server) handleSetAPIKey(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	apiKey := r.FormValue("api_key")
	if apiKey == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther) // Should redirect to edit page?
		return
	}

	if err := s.DB.Model(&data.User{}).Where("username = ?", username).Update("api_key", apiKey).Error; err != nil {
		slog.Error("Failed to update API key", "error", err)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleSetUserRepo(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	repoURL := r.FormValue("app_repo_url")
	// Clone/Update logic (using gitutils)
	// We need `apps_path` for user.
	// user.AppRepoURL is currently just a string in DB.
	// We need to actually clone it?
	// Python: `set_repo(..., apps_path, user.app_repo_url, app_repo_url)`
	// apps_path = users/<user>/apps.
	// But `users/<user>/apps` currently holds uploaded apps too?
	// Python: `apps_path = db.get_users_dir() / user.username / "apps"`
	// If repo is set, it clones INTO `apps`.
	// This might conflict with uploaded apps if not careful.
	// Python replaces the dir if repo changes.

	// In Go:
	// We need `gitutils.CloneOrUpdate`.
	// But first update DB.

	if err := s.DB.Model(&data.User{}).Where("username = ?", username).Update("app_repo_url", repoURL).Error; err != nil {
		slog.Error("Failed to update user repo URL", "error", err)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	appsPath := filepath.Join(s.DataDir, "users", username, "apps")
	if err := gitutils.CloneOrUpdate(appsPath, repoURL); err != nil {
		slog.Error("Failed to sync user repo", "error", err)
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleRefreshUserRepo(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	// Get user to find repo URL
	var user data.User
	s.DB.First(&user, "username = ?", username)

	if user.AppRepoURL != "" {
		appsPath := filepath.Join(s.DataDir, "users", username, "apps")
		if err := gitutils.CloneOrUpdate(appsPath, user.AppRepoURL); err != nil {
			slog.Error("Failed to refresh user repo", "error", err)
		}
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleExportUserConfig(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").Preload("Devices.Apps").Preload("Credentials").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	// Scrub sensitive data
	user.Password = ""
	user.APIKey = "" // Maybe keep? Python exports it? Python removes password.

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s_config.json", username))

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(user); err != nil {
		slog.Error("Failed to export user config", "error", err)
	}
}

func (s *Server) handleExportDeviceConfig(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username)

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}

	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s_config.json", device.Name))

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(device); err != nil {
		slog.Error("Failed to export device config", "error", err)
	}
}

func (s *Server) handleImportUserConfig(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File required", http.StatusBadRequest)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Error("Failed to close uploaded config file", "error", err)
		}
	}()

	var importedUser data.User
	if err := json.NewDecoder(file).Decode(&importedUser); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	var currentUser data.User
	if err := s.DB.Preload("Devices").First(&currentUser, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	// Update fields
	currentUser.Email = importedUser.Email
	currentUser.ThemePreference = importedUser.ThemePreference
	currentUser.SystemRepoURL = importedUser.SystemRepoURL
	currentUser.AppRepoURL = importedUser.AppRepoURL
	if importedUser.APIKey != "" {
		currentUser.APIKey = importedUser.APIKey
	}

	err = s.DB.Transaction(func(tx *gorm.DB) error {
		// Delete existing devices and apps
		var deviceIDs []string
		for _, d := range currentUser.Devices {
			deviceIDs = append(deviceIDs, d.ID)
		}
		if len(deviceIDs) > 0 {
			if err := tx.Where("device_id IN ?", deviceIDs).Delete(&data.App{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id IN ?", deviceIDs).Delete(&data.Device{}).Error; err != nil {
				return err
			}
		}

		if err := tx.Save(&currentUser).Error; err != nil {
			return err
		}

		for _, dev := range importedUser.Devices {
			dev.Username = username
			if err := tx.Create(&dev).Error; err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		slog.Error("Import failed", "error", err)
		http.Error(w, "Import failed", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleSetSystemRepo(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok || username != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	repoURL := r.FormValue("app_repo_url")
	if repoURL == "" {
		repoURL = s.Config.SystemAppsRepo
	}

	appsPath := filepath.Join(s.DataDir, "system-apps")
	if err := gitutils.CloneOrUpdate(appsPath, repoURL); err != nil {
		slog.Error("Failed to update system repo", "error", err)
	}

	// Save to global setting
	if err := s.setSetting("system_apps_repo", repoURL); err != nil {
		slog.Error("Failed to save system repo setting", "error", err)
	}

	// Update in-memory config and cache
	s.Config.SystemAppsRepo = repoURL
	s.RefreshSystemAppsCache()

	http.Redirect(w, r, "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleRefreshSystemRepo(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok || username != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	var user data.User
	s.DB.First(&user, "username = ?", username)

	// Get global repo URL
	repoURL, _ := s.getSetting("system_apps_repo")
	if repoURL == "" {
		repoURL = s.Config.SystemAppsRepo
	}

	appsPath := filepath.Join(s.DataDir, "system-apps")
	if err := gitutils.CloneOrUpdate(appsPath, repoURL); err != nil {
		slog.Error("Failed to refresh system repo", "error", err)
	}

	s.RefreshSystemAppsCache()

	if r.Header.Get("Accept") == "application/json" {
		repoInfo, _ := gitutils.GetRepoInfo(appsPath, repoURL)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(repoInfo); err != nil {
			slog.Error("Failed to encode repo info", "error", err)
		}
		return
	}

	http.Redirect(w, r, "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleUpdateFirmware(w http.ResponseWriter, r *http.Request) {
	err := s.UpdateFirmwareBinaries()
	if err != nil {
		slog.Error("Failed to update firmware binaries", "error", err)
	}

	if r.Header.Get("Accept") == "application/json" {
		version := "unknown"
		firmwareDir := filepath.Join(s.DataDir, "firmware")
		if vBytes, e := os.ReadFile(filepath.Join(firmwareDir, "firmware_version.txt")); e == nil {
			version = strings.TrimSpace(string(vBytes))
		}

		resp := map[string]any{"success": err == nil, "version": version}
		if err != nil {
			resp["error"] = err.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Error("Failed to encode firmware response", "error", err)
		}
		return
	}

	http.Redirect(w, r, "/auth/edit", http.StatusSeeOther)
}

func (s *Server) handleUploadAppGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").First(&user, "username = ?", username).Error; err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	s.renderTemplate(w, r, "uploadapp", TemplateData{User: &user, Device: device})
}

func (s *Server) handleUploadAppPost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").First(&user, "username = ?", username).Error; err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	// Parse multipart
	if err := r.ParseMultipartForm(10 << 20); err != nil { // 10 MB
		http.Error(w, "File too large", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File required", http.StatusBadRequest)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Error("Failed to close uploaded file", "error", err)
		}
	}()

	filename := header.Filename
	ext := filepath.Ext(filename)
	if ext != ".star" && ext != ".webp" {
		http.Error(w, "Invalid file type", http.StatusBadRequest)
		return
	}

	appName := strings.TrimSuffix(filename, ext)

	// Create dir: users/<user>/apps/<appName>
	appDir := filepath.Join(s.DataDir, "users", user.Username, "apps", appName)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		slog.Error("Failed to create app dir", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	dstPath := filepath.Join(appDir, filename)
	dst, err := os.Create(dstPath)
	if err != nil {
		slog.Error("Failed to create dest file", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer func() {
		if err := dst.Close(); err != nil {
			slog.Error("Failed to close destination file", "error", err)
		}
	}()

	if _, err := io.Copy(dst, file); err != nil {
		slog.Error("Failed to copy file", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/devices/%s/addapp", device.ID), http.StatusSeeOther)
}

func (s *Server) handlePasskeysGet(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Credentials").First(&user, "username = ?", username).Error; err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	s.renderTemplate(w, r, "passkeys", TemplateData{User: &user})
}

func (s *Server) handleUpdateDeviceGet(w http.ResponseWriter, r *http.Request) {

	id := r.PathValue("id")

	session, _ := s.Store.Get(r, "session-name")

	username, ok := session.Values["username"].(string)

	if !ok {

		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)

		return

	}

	var user data.User

	if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err != nil {

		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)

		return

	}

	var device *data.Device

	for i := range user.Devices {

		if user.Devices[i].ID == id {

			device = &user.Devices[i]

			break

		}

	}

	if device == nil {

		http.Error(w, "Device not found", http.StatusNotFound)

		return

	}

	// Calculate UI Brightness

	b := device.Brightness

	bUI := 5

	if b == 0 {

		bUI = 0

	} else if b <= 3 {

		bUI = 1

	} else if b <= 5 {

		bUI = 2

	} else if b <= 12 {

		bUI = 3

	} else if b <= 35 {

		bUI = 4

	}

	// Calculate Night Brightness UI

	nb := device.NightBrightness

	nbUI := 0

	if nb == 0 {

		nbUI = 0

	} else if nb <= 3 {

		nbUI = 1

	} else if nb <= 5 {

		nbUI = 2

	} else if nb <= 12 {

		nbUI = 3

	} else if nb <= 35 {

		nbUI = 4

	} else {

		nbUI = 5

	}

	// Calculate Dim Brightness UI

	dbUI := 2 // Default

	if device.DimBrightness != nil {

		val := *device.DimBrightness

		if val == 0 {

			dbUI = 0

		} else if val <= 3 {

			dbUI = 1

		} else if val <= 5 {

			dbUI = 2

		} else if val <= 12 {

			dbUI = 3

		} else if val <= 35 {

			dbUI = 4

		} else {

			dbUI = 5

		}

	}

	// Get available locales

	locales := []string{"en_US", "de_DE"} // Add more as needed or scan directory

	localizer := s.getLocalizer(r)

	s.renderTemplate(w, r, "update", TemplateData{

		User: &user,

		Device: device,

		DeviceTypeChoices: s.getDeviceTypeChoices(localizer),

		ColorFilterChoices: s.getColorFilterChoices(),

		AvailableLocales: locales,

		DefaultImgURL: fmt.Sprintf("/%s/next", device.ID),

		DefaultWsURL: fmt.Sprintf("/%s/ws", device.ID),

		BrightnessUI: bUI,

		NightBrightnessUI: nbUI,

		DimBrightnessUI: dbUI,

		Localizer: localizer,
	})
}

func (s *Server) handleUpdateDevicePost(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").First(&user, "username = ?", username).Error; err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	// 1. Basic Info
	device.Name = r.FormValue("name")
	device.Type = data.DeviceType(r.FormValue("device_type"))
	device.ImgURL = r.FormValue("img_url")
	device.WsURL = r.FormValue("ws_url")
	device.Notes = r.FormValue("notes")

	if i, err := strconv.Atoi(r.FormValue("default_interval")); err == nil {
		device.DefaultInterval = i
	}

	// 2. Color Filter
	colorFilter := r.FormValue("color_filter")
	if colorFilter != "None" {
		val := data.ColorFilter(colorFilter)
		device.ColorFilter = &val
	} else {
		device.ColorFilter = nil
	}

	// 3. Brightness & Scale
	useCustomScale := r.FormValue("use_custom_brightness_scale") == "on"
	customScaleStr := r.FormValue("custom_brightness_scale")
	if useCustomScale {
		device.CustomBrightnessScale = customScaleStr
	} else {
		device.CustomBrightnessScale = ""
	}

	// Parse Scale
	scale := []int{0, 3, 5, 12, 35, 100} // Default
	if device.CustomBrightnessScale != "" {
		parts := strings.Split(device.CustomBrightnessScale, ",")
		if len(parts) == 6 {
			newScale := make([]int, 6)
			valid := true
			for i, p := range parts {
				val, err := strconv.Atoi(strings.TrimSpace(p))
				if err != nil {
					valid = false
					break
				}
				newScale[i] = val
			}
			if valid {
				scale = newScale
			}
		}
	}

	getBrightnessValue := func(uiIndex int) int {
		if uiIndex < 0 {
			uiIndex = 0
		}
		if uiIndex >= len(scale) {
			uiIndex = len(scale) - 1
		}
		return scale[uiIndex]
	}

	if bUI, err := strconv.Atoi(r.FormValue("brightness")); err == nil {
		device.Brightness = data.Brightness(getBrightnessValue(bUI))
	}

	// 4. Interstitial
	device.InterstitialEnabled = r.FormValue("interstitial_enabled") == "on"
	interstitialApp := r.FormValue("interstitial_app")
	if interstitialApp != "None" {
		device.InterstitialApp = &interstitialApp
	} else {
		device.InterstitialApp = nil
	}

	// 5. Night Mode
	device.NightModeEnabled = r.FormValue("night_mode_enabled") == "on"
	device.NightStart = r.FormValue("night_start")
	device.NightEnd = r.FormValue("night_end")

	if nbUI, err := strconv.Atoi(r.FormValue("night_brightness")); err == nil {
		device.NightBrightness = data.Brightness(getBrightnessValue(nbUI))
	}

	nightApp := r.FormValue("night_mode_app")
	if nightApp != "None" {
		device.NightModeApp = nightApp
	} else {
		device.NightModeApp = ""
	}

	nightColorFilter := r.FormValue("night_color_filter")
	if nightColorFilter != "None" {
		val := data.ColorFilter(nightColorFilter)
		device.NightColorFilter = &val
	} else {
		device.NightColorFilter = nil
	}

	// 6. Dim Mode
	dimTime := r.FormValue("dim_time")
	if dimTime != "" {
		device.DimTime = &dimTime
	} else {
		device.DimTime = nil
	}

	if dimUI, err := strconv.Atoi(r.FormValue("dim_brightness")); err == nil {
		val := data.Brightness(getBrightnessValue(dimUI))
		device.DimBrightness = &val
	}

	// 7. Location & Locale
	locationJSON := r.FormValue("location")
	if locationJSON != "" {
		var locMap map[string]any
		if err := json.Unmarshal([]byte(locationJSON), &locMap); err == nil {
			safeStr := func(v any) string {
				if s, ok := v.(string); ok {
					return s
				}
				if f, ok := v.(float64); ok {
					return fmt.Sprintf("%v", f)
				}
				return ""
			}
			device.Location = data.DeviceLocation{
				Description: safeStr(locMap["description"]),
				Lat:         safeStr(locMap["lat"]),
				Lng:         safeStr(locMap["lng"]),
				Locality:    safeStr(locMap["locality"]),
				PlaceID:     safeStr(locMap["place_id"]),
				Timezone:    safeStr(locMap["timezone"]),
			}
		}
	}
	locale := r.FormValue("locale")
	if locale != "" {
		device.Locale = &locale
	} else {
		device.Locale = nil
	}

	// 8. API Key
	device.APIKey = r.FormValue("api_key")

	if err := s.DB.Save(device).Error; err != nil {
		slog.Error("Failed to update device", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDeleteDevice(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var user data.User
	if err := s.DB.Preload("Devices").First(&user, "username = ?", username).Error; err != nil {
		http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
		return
	}

	var device *data.Device
	for i := range user.Devices {
		if user.Devices[i].ID == id {
			device = &user.Devices[i]
			break
		}
	}
	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	// Delete
	if err := s.DB.Delete(device).Error; err != nil {
		slog.Error("Failed to delete device", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDashboardWS(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Dashboard WS upgrade failed", "error", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("Failed to close Dashboard WS connection", "error", err)
		}
	}()

	slog.Debug("Dashboard WS Connected", "username", username)

	// Subscribe to user-specific updates
	ch := s.Broadcaster.Subscribe("user:" + username)
	defer s.Broadcaster.Unsubscribe("user:"+username, ch)

	done := make(chan struct{})

	// Read loop (handle ping/pong/close)
	go func() {
		defer close(done)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					slog.Info("Dashboard WS read error, disconnecting", "username", username, "error", err)
				}
				return
			}
		}
	}()

	// Write loop
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ch:
			// A device/app update has occurred for this user
			// Send a simple message to trigger a full page refresh or AJAX update
			if err := conn.WriteMessage(websocket.TextMessage, []byte("refresh")); err != nil {
				slog.Error("Failed to write refresh message to Dashboard WS", "username", username, "error", err)
				return
			}
		case <-ticker.C:
			// Keep-alive ping
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				slog.Error("Failed to send Dashboard WS ping", "username", username, "error", err)
				return
			}
		}
	}
}

func (s *Server) checkForUpdates() {
	s.doUpdateCheck()
	ticker := time.NewTicker(1 * time.Hour)
	for range ticker.C {
		s.doUpdateCheck()
	}
}

func (s *Server) doUpdateCheck() {
	url := "https://api.github.com/repos/tronbyt/server/releases/latest"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		slog.Debug("Failed to create HTTP request for update check", "error", err)
		return
	}

	githubToken := os.Getenv("GITHUB_TOKEN")
	if githubToken != "" {
		req.Header.Set("Authorization", "Bearer "+githubToken)
	}

	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Log debug only to reduce noise
		slog.Debug("Failed to check for updates", "error", err)
		return
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Debug("Failed to close response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return
	}

	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return
	}

	currentVersion := version.Version
	if !strings.HasPrefix(currentVersion, "v") {
		currentVersion = "v" + currentVersion
	}
	latestVersion := release.TagName
	if !strings.HasPrefix(latestVersion, "v") {
		latestVersion = "v" + latestVersion
	}

	if semver.IsValid(currentVersion) && semver.IsValid(latestVersion) {
		if semver.Compare(latestVersion, currentVersion) > 0 {
			slog.Info("Update available", "current", version.Version, "latest", release.TagName)
			s.UpdateAvailable = true
			s.LatestReleaseURL = release.HTMLURL
		}
	}
}

func (s *Server) updateAppBrokenStatus(w http.ResponseWriter, r *http.Request, broken bool) {
	if s.Config.Production == "1" {
		http.Error(w, "Not allowed in production mode", http.StatusForbidden)
		return
	}

	appName := r.URL.Query().Get("app_name")
	packageName := r.URL.Query().Get("package_name") // Optional

	if appName == "" {
		http.Error(w, "App name is required", http.StatusBadRequest)
		return
	}

	appID := appName
	if packageName != "" && packageName != "None" {
		appID = packageName
	}

	manifestPath := filepath.Join(s.DataDir, "system-apps", "apps", appID, "manifest.yaml")

	// Read existing manifest
	var manifest apps.Manifest
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		slog.Error("Failed to read manifest.yaml", "path", manifestPath, "error", err)
		http.Error(w, "Failed to read manifest", http.StatusInternalServerError)
		return
	}
	if err := yaml.Unmarshal(manifestData, &manifest); err != nil {
		slog.Error("Failed to unmarshal manifest.yaml", "path", manifestPath, "error", err)
		http.Error(w, "Failed to parse manifest", http.StatusInternalServerError)
		return
	}

	// Update status
	manifest.Broken = &broken // Need to take address of bool
	if broken {
		reason := r.URL.Query().Get("broken_reason") // Allow setting reason from query param if needed
		if reason == "" {
			reason = "Marked broken by user" // Default reason
		}
		manifest.BrokenReason = &reason // Need to take address of string
	} else {
		emptyReason := "" // Create a variable for empty string
		manifest.BrokenReason = &emptyReason
	}

	// Write back manifest
	updatedManifestData, err := yaml.Marshal(&manifest)
	if err != nil {
		slog.Error("Failed to marshal updated manifest.yaml", "error", err)
		http.Error(w, "Failed to update manifest", http.StatusInternalServerError)
		return
	}

	if err := os.WriteFile(manifestPath, updatedManifestData, 0644); err != nil {
		slog.Error("Failed to write updated manifest.yaml", "path", manifestPath, "error", err)
		http.Error(w, "Failed to save manifest", http.StatusInternalServerError)
		return
	}

	// Respond with success
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]bool{"success": true}); err != nil {
		slog.Error("Failed to write JSON success response", "error", err)
	}
}
func (s *Server) handleMarkAppBroken(w http.ResponseWriter, r *http.Request) {
	s.updateAppBrokenStatus(w, r, true)
}

func (s *Server) handleUnmarkAppBroken(w http.ResponseWriter, r *http.Request) {
	s.updateAppBrokenStatus(w, r, false)
}
