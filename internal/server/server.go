package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"
	syncer "tronbyt-server/internal/sync"
	"tronbyt-server/web"

	"github.com/dustin/go-humanize"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gorilla/sessions"
	"github.com/gorilla/websocket"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"gorm.io/gorm"
)

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

// Map template names to their file paths relative to web/templates.
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
			end := min(start+length, len(s))
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
			return slices.Contains(slice, item)
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

	s.Router.HandleFunc("GET /devices/{id}/current", s.RequireLogin(s.RequireDevice(s.handleCurrentApp)))
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

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Chain middlewares: Recover -> Logging -> Proxy -> Mux
	RecoverMiddleware(LoggingMiddleware(ProxyMiddleware(s.Router))).ServeHTTP(w, r)
}