package server

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"math/big"
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

type ColorFilterOption struct {
	Value string
	Name  string
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
	ColorFilterOptions []ColorFilterOption
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
	result := s.DB.Limit(1).Find(&setting, "key = ?", key)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", nil
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
		"timeago": func(ts any) string {
			var t time.Time
			switch v := ts.(type) {
			case int64:
				if v == 0 {
					return "never"
				}
				t = time.Unix(v, 0)
			case time.Time:
				if v.IsZero() {
					return "never"
				}
				t = v
			default:
				return "never"
			}
			return humanize.Time(t)
		},
		"duration": func(d any) string {
			var dur time.Duration
			switch v := d.(type) {
			case int64:
				dur = time.Duration(v)
			case time.Duration:
				dur = v
			default:
				return "0s"
			}

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
		"deref": func(v any) string {
			if v == nil {
				return ""
			}
			switch val := v.(type) {
			case *string:
				if val == nil {
					return ""
				}
				return *val
			case *data.ColorFilter:
				if val == nil {
					return ""
				}
				return string(*val)
			default:
				// Fallback for unexpected types
				return fmt.Sprintf("%v", v)
			}
		},
		"derefOr": func(v any, def string) string {
			if v == nil {
				return def
			}
			switch val := v.(type) {
			case *string:
				if val == nil {
					return def
				}
				return *val
			case *data.ColorFilter:
				if val == nil {
					return def
				}
				return string(*val)
			default:
				return fmt.Sprintf("%v", v)
			}
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
		"split": strings.Split,
		"trim":  strings.TrimSpace,
		"slice": func(args ...string) []string {
			return args
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

// saveSession saves the session with dynamic Secure flag based on request scheme.
func (s *Server) saveSession(w http.ResponseWriter, r *http.Request, session *sessions.Session) error {
	// Create a copy of options to modify safely
	opts := *session.Options
	opts.Secure = r.URL.Scheme == "https"
	session.Options = &opts

	return session.Save(r, w)
}

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
		s.Router.Handle("GET /static/favicon.ico", http.StripPrefix("/static/", fileServer))
	}

	// App Preview (Specific path)
	s.Router.HandleFunc("GET /preview/app/{id}", s.handleSystemAppThumbnail)

	s.SetupAPIRoutes()
	s.SetupAuthRoutes()

	// Device endpoint
	s.Router.Handle("GET /{id}/next", http.HandlerFunc(s.handleNextApp))

	// Web UI
	s.Router.HandleFunc("GET /", s.RequireLogin(s.handleIndex))
	s.Router.HandleFunc("GET /admin", s.RequireLogin(s.handleAdminIndex))
	s.Router.HandleFunc("DELETE /admin/users/{username}", s.RequireLogin(s.handleDeleteUser))

	s.Router.HandleFunc("GET /devices/create", s.RequireLogin(s.handleCreateDeviceGet))
	s.Router.HandleFunc("POST /devices/create", s.RequireLogin(s.handleCreateDevicePost))

	s.Router.HandleFunc("GET /devices/{id}/addapp", s.RequireLogin(s.RequireDevice(s.handleAddAppGet)))
	s.Router.HandleFunc("POST /devices/{id}/addapp", s.RequireLogin(s.RequireDevice(s.handleAddAppPost)))

	s.Router.HandleFunc("POST /devices/{id}/{iname}/delete", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleDeleteApp))))
	s.Router.HandleFunc("GET /devices/{id}/{iname}/config", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleConfigAppGet))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/config", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleConfigAppPost))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/schema_handler/{handler}", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleSchemaHandler))))

	s.Router.HandleFunc("POST /devices/{id}/{iname}/toggle_pin", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleTogglePin))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/toggle_enabled", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleToggleEnabled))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/moveapp", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleMoveApp))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/duplicate", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleDuplicateApp))))
	s.Router.HandleFunc("POST /devices/{target_device_id}/apps/duplicate_from/{source_device_id}/{iname}", s.RequireLogin(s.RequireDevice(s.handleDuplicateAppToDevice)))
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
	s.Router.HandleFunc("POST /update_firmware", s.RequireLogin(s.handleUpdateFirmware))

	// App broken status (development only)
	s.Router.HandleFunc("POST /mark_app_broken", s.RequireLogin(s.handleMarkAppBroken))
	s.Router.HandleFunc("POST /unmark_app_broken", s.RequireLogin(s.handleUnmarkAppBroken))

	s.Router.HandleFunc("GET /devices/{id}/current", s.handleCurrentApp)
	s.Router.HandleFunc("GET /devices/{id}/installations/{iname}/preview", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleRenderConfigPreview))))
	s.Router.HandleFunc("POST /devices/{id}/{iname}/preview", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handlePushPreview))))

	// Firmware
	s.Router.HandleFunc("GET /devices/{id}/firmware", s.RequireLogin(s.RequireDevice(s.handleFirmwareGenerateGet)))
	s.Router.HandleFunc("POST /devices/{id}/firmware", s.RequireLogin(s.RequireDevice(s.handleFirmwareGeneratePost)))

	s.Router.HandleFunc("GET /devices/{id}/update", s.RequireLogin(s.RequireDevice(s.handleUpdateDeviceGet)))
	s.Router.HandleFunc("POST /devices/{id}/update", s.RequireLogin(s.RequireDevice(s.handleUpdateDevicePost)))
	s.Router.HandleFunc("POST /devices/{id}/delete", s.RequireLogin(s.RequireDevice(s.handleDeleteDevice)))
	s.Router.HandleFunc("POST /devices/{id}/import_config", s.RequireLogin(s.RequireDevice(s.handleImportDeviceConfig)))

	// Websocket routes
	s.SetupWebsocketRoutes()
	s.Router.HandleFunc("GET /health", s.handleHealth)
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
	if tmplData.User == nil {
		if username, ok := session.Values["username"].(string); ok {
			var user data.User
			if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "username = ?", username).Error; err == nil {
				tmplData.User = &user
			}
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

	if err := s.saveSession(w, r, session); err != nil {
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

	allDeviceTypes := []data.DeviceType{
		data.DeviceTidbytGen1,
		data.DeviceTidbytGen2,
		data.DevicePixoticker,
		data.DeviceRaspberryPi,
		data.DeviceRaspberryPiWide,
		data.DeviceTronbytS3,
		data.DeviceTronbytS3Wide,
		data.DeviceMatrixPortal,
		data.DeviceMatrixPortalWS,
		data.DeviceOther,
	}

	for _, dt := range allDeviceTypes {
		choices[string(dt)] = s.localizeOrID(localizer, dt.String())
	}
	return choices
}

// --- Handlers ---

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	slog.Debug("handleIndex called")
	user := GetUser(r)

	var devicesWithUI []DeviceWithUIScale

	for i := range user.Devices {
		device := &user.Devices[i]
		slog.Debug("handleIndex device", "id", device.ID, "apps_count", len(device.Apps))

		// Sort Apps
		sort.Slice(device.Apps, func(i, j int) bool {
			return device.Apps[i].Order < device.Apps[j].Order
		})

		// Calculate UI Brightness
		var customScale map[int]int
		if device.CustomBrightnessScale != "" {
			customScale = data.ParseCustomBrightnessScale(device.CustomBrightnessScale)
		}
		bUI := device.Brightness.UIScale(customScale)

		devicesWithUI = append(devicesWithUI, DeviceWithUIScale{
			Device:       device,
			BrightnessUI: bUI,
		})
	}

	s.renderTemplate(w, r, "index", TemplateData{User: user, DevicesWithUIScales: devicesWithUI})
}

func (s *Server) handleAdminIndex(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user.Username != "admin" {
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
	user := GetUser(r)
	if user.Username != "admin" {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if targetUsername == user.Username {
		http.Error(w, "Cannot delete yourself", http.StatusBadRequest)
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

	http.Redirect(w, r, "/admin", http.StatusSeeOther)
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

func (s *Server) getColorFilterChoices() []ColorFilterOption {
	return []ColorFilterOption{
		{Value: "none", Name: "None"},
		{Value: "dimmed", Name: "Dimmed"},
		{Value: "redshift", Name: "Redshift"},
		{Value: "warm", Name: "Warm"},
		{Value: "sunset", Name: "Sunset"},
		{Value: "sepia", Name: "Sepia"},
		{Value: "vintage", Name: "Vintage"},
		{Value: "dusk", Name: "Dusk"},
		{Value: "cool", Name: "Cool"},
		{Value: "bw", Name: "Black & White"},
		{Value: "ice", Name: "Ice"},
		{Value: "moonlight", Name: "Moonlight"},
		{Value: "neon", Name: "Neon"},
		{Value: "pastel", Name: "Pastel"},
	}
}

func (s *Server) handleSetThemePreference(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

	theme := r.FormValue("theme")
	if theme == "" {
		http.Error(w, "Theme required", http.StatusBadRequest)
		return
	}

	if err := s.DB.Model(&data.User{}).Where("username = ?", user.Username).Update("theme_preference", theme).Error; err != nil {
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
	user := GetUser(r)

	localizer := s.getLocalizer(r)
	s.renderTemplate(w, r, "create", TemplateData{
		User:              user,
		DeviceTypeChoices: s.getDeviceTypeChoices(localizer),
		Localizer:         localizer,
		Form:              CreateDeviceFormData{Brightness: data.Brightness(20).UIScale(nil)}, // Default brightness 20%
	})
}

func (s *Server) handleCreateDevicePost(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

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
			User:              user,
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
				User:              user,
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

	// Default to 'None' color filter
	defaultColorFilter := data.ColorFilterNone
	newDevice.ColorFilter = &defaultColorFilter

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

// duplicateAppToDeviceLogic handles the core logic of duplicating an app.
func (s *Server) duplicateAppToDeviceLogic(r *http.Request, user *data.User, sourceDevice *data.Device, originalApp *data.App, targetDevice *data.Device, targetIname string, insertAfter bool) error {
	// Generate a unique iname for the duplicate on the target device
	var newIname string
	maxAttempts := 900 // Max 900 attempts for 3-digit numbers
	for i := 0; i < maxAttempts; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(900)) // 0-899
		if err != nil {
			return fmt.Errorf("failed to generate random number for iname: %w", err)
		}
		candidateIname := fmt.Sprintf("%d", n.Int64()+100) // 100-999

		// Check if this iname already exists on the target device
		found := false
		for _, app := range targetDevice.Apps {
			if app.Iname == candidateIname {
				found = true
				break
			}
		}
		if !found {
			newIname = candidateIname
			break
		}
	}
	if newIname == "" {
		return errors.New("error generating unique ID: No available IDs in the 100-999 range")
	}

	// Create a copy of the original app
	duplicatedApp := *originalApp // Shallow copy, then deep copy map/pointers
	duplicatedApp.ID = 0          // Let GORM generate a new primary key
	duplicatedApp.DeviceID = targetDevice.ID
	duplicatedApp.Iname = newIname
	duplicatedApp.LastRender = time.Time{} // Reset render time for new app
	duplicatedApp.Order = 0                // Will be set below
	duplicatedApp.AutoPin = false          // Do not auto-pin duplicated app

	// Deep copy Config map if it exists
	if originalApp.Config != nil {
		newConfig := make(data.JSONMap)
		for k, v := range originalApp.Config {
			newConfig[k] = v
		}
		duplicatedApp.Config = newConfig
	}

	// If it's a non-pushed app, copy its .star file
	if !originalApp.Pushed && originalApp.Path != nil && *originalApp.Path != "" {
		sourcePath := s.resolveAppPath(*originalApp.Path)
		installDir := filepath.Join(s.DataDir, "installations", newIname)
		if err := os.MkdirAll(installDir, 0755); err != nil {
			return fmt.Errorf("failed to create install dir for duplicated app: %w", err)
		}
		destPath := filepath.Join(installDir, fmt.Sprintf("%s.star", newIname))

		if err := copyFile(sourcePath, destPath); err != nil {
			return fmt.Errorf("failed to copy .star file for duplicated app: %w", err)
		}
		relPath := filepath.Join("installations", newIname, fmt.Sprintf("%s.star", newIname))
		duplicatedApp.Path = &relPath
	}

	// Logic to insert at correct position in target device
	// Get all apps sorted by order
	appsList := make([]data.App, len(targetDevice.Apps))
	copy(appsList, targetDevice.Apps)
	sort.Slice(appsList, func(i, j int) bool {
		return appsList[i].Order < appsList[j].Order
	})

	// Find insertion point
	targetIdx := -1
	if targetIname != "" {
		for i, appItem := range appsList {
			if appItem.Iname == targetIname {
				targetIdx = i
				break
			}
		}
	}

	if targetIdx != -1 {
		insertIdx := targetIdx + 1
		if !insertAfter {
			insertIdx = targetIdx
		}
		// Insert (Go slices)
		appsList = append(appsList[:insertIdx], append([]data.App{duplicatedApp}, appsList[insertIdx:]...)...)
	} else {
		// If no valid target found or empty list, append to end
		appsList = append(appsList, duplicatedApp)
	}

	// Update order for all apps
	for i := range appsList {
		appsList[i].Order = i
	}
	targetDevice.Apps = appsList

	// If the app is a pushed app (uploaded image), copy the image file
	if originalApp.Pushed {
		sourceWebpDir := s.getDeviceWebPDir(sourceDevice.ID)
		sourceWebpPath := filepath.Join(sourceWebpDir, "pushed", fmt.Sprintf("%s.webp", originalApp.Iname))

		targetWebpDir := s.getDeviceWebPDir(targetDevice.ID)
		targetPushedWebpDir := filepath.Join(targetWebpDir, "pushed")
		if err := os.MkdirAll(targetPushedWebpDir, 0755); err != nil {
			slog.Error("Failed to create target pushed webp directory", "path", targetPushedWebpDir, "error", err)
			// Continue, don't fail entire operation just for image copy failure
		}
		targetWebpPath := filepath.Join(targetPushedWebpDir, fmt.Sprintf("%s.webp", newIname))

		if _, err := os.Stat(sourceWebpPath); err == nil {
			if err := copyFile(sourceWebpPath, targetWebpPath); err != nil {
				slog.Error("Failed to copy pushed image", "source", sourceWebpPath, "target", targetWebpPath, "error", err)
			} else {
				slog.Info("Copied pushed image", "from", sourceWebpPath, "to", targetWebpPath)
			}
		} else if !os.IsNotExist(err) {
			slog.Error("Failed to stat source pushed image", "path", sourceWebpPath, "error", err)
		} else {
			slog.Warn("Source pushed image not found", "path", sourceWebpPath)
		}
	}

	// Save the user data (which includes devices and their apps)
	if err := s.DB.Save(user).Error; err != nil {
		return fmt.Errorf("failed to save user after duplicating app: %w", err)
	}

	// Trigger initial render for the duplicated app
	s.possiblyRender(r.Context(), &duplicatedApp, targetDevice, user)

	return nil
}

// handleDuplicateAppToDevice handles the HTTP request for duplicating an app across devices.
func (s *Server) handleDuplicateAppToDevice(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r) // Assumes RequireLogin middleware sets user

	sourceDeviceID := r.PathValue("source_device_id")
	targetDeviceID := r.PathValue("target_device_id")
	iname := r.PathValue("iname") // Source app iname

	targetIname := r.FormValue("target_iname")
	insertAfterStr := r.FormValue("insert_after")
	insertAfter := insertAfterStr == "true"

	if sourceDeviceID == "" || targetDeviceID == "" || iname == "" {
		http.Error(w, "Missing required parameters", http.StatusBadRequest)
		return
	}

	var sourceDevice *data.Device
	var targetDevice *data.Device
	var originalApp *data.App

	// Find source and target devices
	for i := range user.Devices {
		if user.Devices[i].ID == sourceDeviceID {
			sourceDevice = &user.Devices[i]
		}
		if user.Devices[i].ID == targetDeviceID {
			targetDevice = &user.Devices[i]
		}
	}

	if sourceDevice == nil {
		http.Error(w, "Source device not found", http.StatusNotFound)
		return
	}
	if targetDevice == nil {
		http.Error(w, "Target device not found", http.StatusNotFound)
		return
	}

	// Find the original app on the source device
	for i := range sourceDevice.Apps {
		if sourceDevice.Apps[i].Iname == iname {
			originalApp = &sourceDevice.Apps[i]
			break
		}
	}

	if originalApp == nil {
		http.Error(w, "Source app not found on device", http.StatusNotFound)
		return
	}

	if err := s.duplicateAppToDeviceLogic(r, user, sourceDevice, originalApp, targetDevice, targetIname, insertAfter); err != nil {
		slog.Error("Failed to duplicate app", "error", err)
		http.Error(w, fmt.Sprintf("Failed to duplicate app: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		slog.Error("Failed to write OK response", "error", err)
	}
}

func (s *Server) handleAddAppGet(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

	s.SystemAppsCacheMutex.RLock()
	systemApps := make([]apps.AppMetadata, len(s.SystemAppsCache))
	copy(systemApps, s.SystemAppsCache)
	s.SystemAppsCacheMutex.RUnlock()

	customApps, err := apps.ListUserApps(s.DataDir, user.Username)
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
		User:           user,
		Device:         device,
		SystemApps:     systemApps,
		CustomApps:     customApps,
		SystemRepoInfo: systemRepoInfo,
		Config:         &config.TemplateConfig{Production: s.Config.Production},
	})
}

func (s *Server) handleAddAppPost(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

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

	// Generate iname (random 3-digit string, matching Python version)
	var iname string
	for i := 0; i < 100; i++ {
		// Random integer between 100 and 999
		n, err := rand.Int(rand.Reader, big.NewInt(900))
		if err != nil {
			slog.Error("Failed to generate random number", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		iname = fmt.Sprintf("%d", n.Int64()+100)

		var count int64
		if err := s.DB.Model(&data.App{}).Where("device_id = ? AND iname = ?", device.ID, iname).Count(&count).Error; err != nil {
			slog.Error("Failed to check iname uniqueness", "error", err)
			break
		}
		if count == 0 {
			break
		}
		if i == 99 {
			slog.Error("Could not generate unique iname")
			http.Error(w, "Could not generate unique iname", http.StatusInternalServerError)
			return
		}
	}

	// Create App in DB
	newApp := data.App{
		DeviceID:    device.ID,
		Iname:       iname,
		Name:        appName,
		UInterval:   uinterval,
		DisplayTime: displayTime,
		Notes:       notes,
		Enabled:     true,
		Path:        &appPath,
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

	// Trigger initial render
	s.possiblyRender(r.Context(), &newApp, device, user)

	http.Redirect(w, r, fmt.Sprintf("/devices/%s/%s/config?delete_on_cancel=true", device.ID, newApp.Iname), http.StatusSeeOther)
}

func (s *Server) handleSystemAppThumbnail(w http.ResponseWriter, r *http.Request) {
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
	user := GetUser(r)
	device := GetDevice(r)
	app := GetApp(r)

	// Get Schema
	var schemaBytes []byte
	if app.Path != nil && *app.Path != "" {
		appPath := s.resolveAppPath(*app.Path)
		var err error
		schemaBytes, err = renderer.GetSchema(appPath, 64, 32, device.Type.Supports2x())
		if err != nil {
			slog.Error("Failed to get app schema", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	if len(schemaBytes) == 0 {
		schemaBytes = []byte("{}")
	}

	deleteOnCancel := r.URL.Query().Get("delete_on_cancel") == "true"

	s.renderTemplate(w, r, "configapp", TemplateData{
		User:               user,
		Device:             device,
		App:                app,
		Schema:             template.JS(schemaBytes),
		AppConfig:          app.Config,
		DeleteOnCancel:     deleteOnCancel,
		ColorFilterOptions: s.getColorFilterChoices(),
	})
}

func (s *Server) handleConfigAppPost(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)
	app := GetApp(r)

	// Parse JSON body
	var payload struct {
		Enabled             bool           `json:"enabled"`
		AutoPin             bool           `json:"autopin"`
		UInterval           int            `json:"uinterval"`
		DisplayTime         int            `json:"display_time"`
		Notes               string         `json:"notes"`
		Config              map[string]any `json:"config"`
		UseCustomRecurrence bool           `json:"use_custom_recurrence"`
		ColorFilter         string         `json:"color_filter"`
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

	if payload.ColorFilter != "" {
		if payload.ColorFilter == "inherit" {
			app.ColorFilter = nil
		} else {
			val := data.ColorFilter(payload.ColorFilter)
			app.ColorFilter = &val
		}
	}

	// Save to DB
	if err := s.DB.Save(app).Error; err != nil {
		slog.Error("Failed to save app config", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Trigger Render
	s.possiblyRender(r.Context(), app, device, user)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleSchemaHandler(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)
	app := GetApp(r)
	handler := r.PathValue("handler")

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

	// Call Handler
	result, err := renderer.CallSchemaHandler(
		r.Context(),
		appPath,
		configStr,
		64, 32,
		device.Type.Supports2x(),
		handler,
		payload.Param)
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
	// Auth check
	session, _ := s.Store.Get(r, "session-name")
	if _, ok := session.Values["username"].(string); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var device data.Device
	if err := s.DB.Preload("Apps").First(&device, "id = ?", id).Error; err != nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

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

func (s *Server) handleRenderConfigPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	iname := r.PathValue("iname")

	// Auth check (if manual) or rely on middleware if added.
	// Since routes were updated with RequireApp, we should use GetDevice/GetApp.
	// But let's check if the route change was successful and applied.
	// Yes, `s.Router.HandleFunc("GET /devices/{id}/installations/{iname}/preview", s.RequireLogin(s.RequireDevice(s.RequireApp(s.handleRenderConfigPreview))))`
	// So we can use helpers.

	device := GetDevice(r)
	app := GetApp(r)

	// Check for 'config' query param
	configParam := r.URL.Query().Get("config")
	if configParam != "" {
		// Render on-the-fly
		var configData map[string]any
		if err := json.Unmarshal([]byte(configParam), &configData); err != nil {
			http.Error(w, "Invalid config JSON", http.StatusBadRequest)
			return
		}

		config := make(map[string]string)
		for k, v := range configData {
			config[k] = fmt.Sprintf("%v", v)
		}

		if app.Path == nil || *app.Path == "" {
			http.Error(w, "App path not set", http.StatusBadRequest)
			return
		}
		appPath := s.resolveAppPath(*app.Path)

		// Defaults
		appInterval := app.DisplayTime
		if appInterval == 0 {
			appInterval = device.DefaultInterval
		}

		// Filters
		var filters []string
		if app.ColorFilter != nil && *app.ColorFilter != "" {
			filters = append(filters, string(*app.ColorFilter))
		}

		deviceTimezone := device.GetTimezone()

		imgBytes, _, err := renderer.Render(
			r.Context(),
			appPath,
			config,
			64, 32,
			time.Duration(appInterval)*time.Second,
			30*time.Second,
			true,
			device.Type.Supports2x(),
			&deviceTimezone,
			device.Locale,
			filters,
		)
		if err != nil {
			slog.Error("Preview render failed", "error", err)
			http.Error(w, "Render failed", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "image/webp")
		w.Header().Set("Cache-Control", "no-cache")
		if _, err := w.Write(imgBytes); err != nil {
			slog.Error("Failed to write image bytes for render config preview", "error", err)
		}
		return
	}

	// Fallback to serving existing file
	webpDir := s.getDeviceWebPDir(id)
	filename := fmt.Sprintf("%s-%s.webp", app.Name, app.Iname)
	path := filepath.Join(webpDir, filename)

	if _, err := os.Stat(path); os.IsNotExist(err) {
		path = filepath.Join(webpDir, "pushed", iname+".webp")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			s.sendDefaultImage(w, r, device)
			return
		}
	}

	http.ServeFile(w, r, path)
}

func (s *Server) handlePushPreview(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)
	app := GetApp(r)

	// Parse Config from Body
	var configBody map[string]any
	if r.Body != nil {
		err := json.NewDecoder(r.Body).Decode(&configBody)
		if err != nil && err != io.EOF {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	}

	// Convert to string values
	config := make(map[string]string)
	for k, v := range configBody {
		config[k] = fmt.Sprintf("%v", v)
	}

	// Render
	if app.Path == nil || *app.Path == "" {
		http.Error(w, "App path not set", http.StatusBadRequest)
		return
	}
	appPath := s.resolveAppPath(*app.Path)

	appInterval := app.DisplayTime
	if appInterval == 0 {
		appInterval = device.DefaultInterval
	}

	// Filters
	var filters []string
	if app.ColorFilter != nil && *app.ColorFilter != "" {
		filters = append(filters, string(*app.ColorFilter))
	}

	deviceTimezone := device.GetTimezone()

	imgBytes, messages, err := renderer.Render(
		r.Context(),
		appPath,
		config,
		64, 32,
		time.Duration(appInterval)*time.Second,
		30*time.Second,
		true,
		device.Type.Supports2x(),
		&deviceTimezone,
		device.Locale,
		filters,
	)
	if err != nil {
		slog.Error("Preview render failed", "error", err)
		http.Error(w, "Render failed", http.StatusInternalServerError)
		return
	}
	for _, msg := range messages {
		slog.Info("Preview render message", "message", msg)
	}

	// Push preview image to device (ephemeral)
	if err := s.savePushedImage(device.ID, app.Iname, imgBytes); err != nil {
		http.Error(w, "Failed to push preview", http.StatusInternalServerError)
		return
	}

	// Notify device via Websocket (Broadcaster)
	s.Broadcaster.Notify(device.ID)

	w.WriteHeader(http.StatusOK)
}

// API Handlers

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)
	app := GetApp(r)

	// Delete App
	if err := s.DB.Delete(app).Error; err != nil {
		slog.Error("Failed to delete app", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Clean up webp
	webpDir := s.getDeviceWebPDir(device.ID)
	matches, _ := filepath.Glob(filepath.Join(webpDir, fmt.Sprintf("*-%s.webp", app.Iname)))
	for _, match := range matches {
		if err := os.Remove(match); err != nil {
			slog.Error("Failed to remove app webp file", "path", match, "error", err)
		}
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleTogglePin(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)
	app := GetApp(r)

	// Logic
	newPinned := app.Iname
	if device.PinnedApp != nil && *device.PinnedApp == app.Iname {
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
	s.Broadcaster.Notify("user:" + user.Username)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleToggleEnabled(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	app := GetApp(r)

	app.Enabled = !app.Enabled
	if err := s.DB.Model(app).Update("enabled", app.Enabled).Error; err != nil {
		slog.Error("Failed to toggle enabled", "error", err)
	}

	// Notify Dashboard
	s.Broadcaster.Notify("user:" + user.Username)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleMoveApp(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)
	targetApp := GetApp(r) // Ensures existence
	direction := r.FormValue("direction")

	// Sort
	appsList := make([]data.App, len(device.Apps))
	copy(appsList, device.Apps)
	sort.Slice(appsList, func(i, j int) bool {
		return appsList[i].Order < appsList[j].Order
	})

	idx := -1
	for i, app := range appsList {
		if app.Iname == targetApp.Iname {
			idx = i
			break
		}
	}

	if idx == -1 {
		// Should not happen due to RequireApp
		http.Error(w, "App not found in sorted list", http.StatusInternalServerError)
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
	s.Broadcaster.Notify("user:" + user.Username)

	w.WriteHeader(http.StatusOK)
}
func (s *Server) handleDuplicateApp(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)
	originalApp := GetApp(r)

	// Generate new iname (random 3-digit string)
	var newIname string
	for i := 0; i < 100; i++ {
		// Random integer between 100 and 999
		n, err := rand.Int(rand.Reader, big.NewInt(900))
		if err != nil {
			slog.Error("Failed to generate random number", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		newIname = fmt.Sprintf("%d", n.Int64()+100)

		var count int64
		if err := s.DB.Model(&data.App{}).Where("device_id = ? AND iname = ?", device.ID, newIname).Count(&count).Error; err != nil {
			slog.Error("Failed to check iname uniqueness", "error", err)
			break
		}
		if count == 0 {
			break
		}
		if i == 99 {
			http.Error(w, "Could not generate unique iname", http.StatusInternalServerError)
			return
		}
	}

	// Copy App
	newApp := *originalApp
	newApp.ID = 0 // GORM will generate new ID
	newApp.Iname = newIname
	newApp.LastRender = time.Time{}
	newApp.Order = originalApp.Order + 1
	newApp.Pushed = false

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
	s.Broadcaster.Notify("user:" + user.Username)

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleReorderApps(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)
	draggedIname := r.FormValue("dragged_iname")
	targetIname := r.FormValue("target_iname")
	insertAfter := r.FormValue("insert_after") == "true"

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
		http.Error(w, "App not found", http.StatusBadRequest)
		return
	}

	// Reorder logic (Bubble up/down)
	// Simple approach: Remove dragged, Insert at target
	app := appsList[draggedIdx]
	// Remove
	appsList = append(appsList[:draggedIdx], appsList[draggedIdx+1:]...)

	// New Target Index
	// Since we removed one, if draggedIdx < targetIdx, targetIdx shifts down by 1
	if draggedIdx < targetIdx {
		targetIdx--
	}

	if insertAfter {
		targetIdx++
	}

	// Insert
	if targetIdx >= len(appsList) {
		appsList = append(appsList, app)
	} else {
		appsList = append(appsList[:targetIdx+1], appsList[targetIdx:]...)
		appsList[targetIdx] = app
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
		slog.Error("Failed to reorder apps", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Notify Dashboard
	s.Broadcaster.Notify("user:" + user.Username)

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
	// We need `gitutils.EnsureRepo`.
	// But first update DB.

	if err := s.DB.Model(&data.User{}).Where("username = ?", username).Update("app_repo_url", repoURL).Error; err != nil {
		slog.Error("Failed to update user repo URL", "error", err)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	appsPath := filepath.Join(s.DataDir, "users", username, "apps")
	if err := gitutils.EnsureRepo(appsPath, repoURL, true); err != nil {
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
		if err := gitutils.EnsureRepo(appsPath, user.AppRepoURL, true); err != nil {
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
	if err := gitutils.EnsureRepo(appsPath, repoURL, true); err != nil {
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
	if err := gitutils.EnsureRepo(appsPath, repoURL, true); err != nil {
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
	user := GetUser(r)
	device := GetDevice(r)

	s.renderTemplate(w, r, "uploadapp", TemplateData{User: user, Device: device})
}

func (s *Server) handleUploadAppPost(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

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

func (s *Server) handleUpdateDeviceGet(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

	// Parse custom brightness scale if device has one
	var customScale map[int]int
	if device.CustomBrightnessScale != "" {
		customScale = data.ParseCustomBrightnessScale(device.CustomBrightnessScale)
	}

	// Calculate UI Brightness
	bUI := device.Brightness.UIScale(customScale)

	// Calculate Night Brightness UI
	nbUI := device.NightBrightness.UIScale(customScale)

	// Calculate Dim Brightness UI
	dbUI := 2 // Default
	if device.DimBrightness != nil {
		dbUI = (*device.DimBrightness).UIScale(customScale)
	}

	// Get available locales
	locales := []string{"en_US", "de_DE"} // Add more as needed or scan directory
	localizer := s.getLocalizer(r)

	// Determine scheme and host for default URLs
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	wsScheme := "ws"
	if scheme == "https" {
		wsScheme = "wss"
	}
	host := r.Host

	s.renderTemplate(w, r, "update", TemplateData{
		User:               user,
		Device:             device,
		DeviceTypeChoices:  s.getDeviceTypeChoices(localizer),
		ColorFilterOptions: s.getColorFilterChoices(),
		AvailableLocales:   locales,
		DefaultImgURL:      fmt.Sprintf("%s://%s/%s/next", scheme, host, device.ID),
		DefaultWsURL:       fmt.Sprintf("%s://%s/%s/ws", wsScheme, host, device.ID),
		BrightnessUI:       bUI,
		NightBrightnessUI:  nbUI,
		DimBrightnessUI:    dbUI,

		Localizer: localizer,
	})
}

func (s *Server) handleUpdateDevicePost(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

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
	if colorFilter != "none" {
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
	var customScale map[int]int
	if device.CustomBrightnessScale != "" {
		customScale = data.ParseCustomBrightnessScale(device.CustomBrightnessScale)
	}

	if bUI, err := strconv.Atoi(r.FormValue("brightness")); err == nil {
		device.Brightness = data.BrightnessFromUIScale(bUI, customScale)
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
		device.NightBrightness = data.BrightnessFromUIScale(nbUI, customScale)
	}

	nightApp := r.FormValue("night_mode_app")
	if nightApp != "None" {
		device.NightModeApp = nightApp
	} else {
		device.NightModeApp = ""
	}

	nightColorFilter := r.FormValue("night_color_filter")
	if nightColorFilter != "none" {
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
		val := data.BrightnessFromUIScale(dimUI, customScale)
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
	device := GetDevice(r)

	// Delete
	if err := s.DB.Delete(device).Error; err != nil {
		slog.Error("Failed to delete device", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
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
func (s *Server) handleImportDeviceConfig(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

	// Max 1MB file size
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		slog.Error("Failed to parse multipart form for device import", "error", err)
		http.Error(w, "File upload failed: invalid form data", http.StatusBadRequest)
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		slog.Error("Failed to get uploaded file for device import", "error", err)
		http.Error(w, "File upload failed", http.StatusBadRequest)
		return
	}
	defer func() {
		if err := file.Close(); err != nil {
			slog.Error("Failed to close uploaded device config file", "error", err)
		}
	}()

	var importedDevice data.Device
	if err := json.NewDecoder(file).Decode(&importedDevice); err != nil {
		slog.Error("Failed to decode imported device JSON", "error", err)
		http.Error(w, "Invalid JSON file", http.StatusBadRequest)
		return
	}

	// Begin a transaction to ensure atomicity
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		// 1. Delete existing apps for this device
		if err := tx.Where("device_id = ?", device.ID).Delete(&data.App{}).Error; err != nil {
			return fmt.Errorf("failed to delete existing apps: %w", err)
		}

		// 2. Update device fields with imported data (excluding ID, Username, APIKey)
		device.Name = importedDevice.Name
		device.Type = importedDevice.Type
		device.ImgURL = importedDevice.ImgURL
		device.WsURL = importedDevice.WsURL
		device.Notes = importedDevice.Notes
		device.Brightness = importedDevice.Brightness
		device.CustomBrightnessScale = importedDevice.CustomBrightnessScale
		device.NightModeEnabled = importedDevice.NightModeEnabled
		device.NightModeApp = importedDevice.NightModeApp
		device.NightStart = importedDevice.NightStart
		device.NightEnd = importedDevice.NightEnd
		device.NightBrightness = importedDevice.NightBrightness
		device.DimTime = importedDevice.DimTime
		device.DimBrightness = importedDevice.DimBrightness
		device.DefaultInterval = importedDevice.DefaultInterval
		device.Timezone = importedDevice.Timezone
		device.Locale = importedDevice.Locale
		device.Location = importedDevice.Location
		device.LastAppIndex = importedDevice.LastAppIndex
		device.PinnedApp = importedDevice.PinnedApp
		device.InterstitialEnabled = importedDevice.InterstitialEnabled
		device.InterstitialApp = importedDevice.InterstitialApp
		device.LastSeen = importedDevice.LastSeen
		device.Info = importedDevice.Info
		device.ColorFilter = importedDevice.ColorFilter
		device.NightColorFilter = importedDevice.NightColorFilter

		if err := tx.Save(device).Error; err != nil {
			return fmt.Errorf("failed to save updated device: %w", err)
		}

		// 3. Create new apps from imported device
		for _, app := range importedDevice.Apps {
			app.DeviceID = device.ID // Ensure DeviceID is set to the current device's ID
			app.ID = 0               // GORM will assign a new primary key
			if err := tx.Create(&app).Error; err != nil {
				return fmt.Errorf("failed to create imported app '%s': %w", app.Name, err)
			}
		}

		return nil
	})

	if err != nil {
		slog.Error("Device import transaction failed", "device_id", device.ID, "error", err)
		http.Error(w, fmt.Sprintf("Import failed: %s", err.Error()), http.StatusInternalServerError)
		return
	}

	slog.Info("Device config imported successfully", "device_id", device.ID, "username", user.Username)
	http.Redirect(w, r, fmt.Sprintf("/devices/%s/update", device.ID), http.StatusSeeOther)
}

func (s *Server) handleMarkAppBroken(w http.ResponseWriter, r *http.Request) {
	s.updateAppBrokenStatus(w, r, true)
}

func (s *Server) handleUnmarkAppBroken(w http.ResponseWriter, r *http.Request) {
	s.updateAppBrokenStatus(w, r, false)
}
