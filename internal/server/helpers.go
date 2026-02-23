package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/gitutils"
	"tronbyt-server/internal/version"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/gorilla/sessions"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ColorFilterOption struct {
	Value string
	Name  string
}

type DeviceTypeOption struct {
	Value data.DeviceType
	Label string
}

// DeviceSummary is a lightweight struct for "Copy to" dropdown targets.
type DeviceSummary struct {
	ID   string
	Name string
}

// TemplateData is a struct to pass data to HTML templates.
type TemplateData struct {
	User       *data.User
	Users      []data.User // For admin view
	Config     *config.TemplateConfig
	Flashes    []string
	Devices    []data.Device   // Filtered devices for rendering
	AllDevices []DeviceSummary // Lightweight list for "Copy to" dropdown
	Item       *data.Device    // For single item partials
	Localizer  *i18n.Localizer

	UpdateAvailable  bool
	LatestReleaseURL string

	// Page-specific data
	Device            *data.Device
	SystemApps        []apps.AppMetadata
	CustomApps        []apps.AppMetadata
	DeviceTypeChoices []DeviceTypeOption
	Form              CreateDeviceFormData

	// Repo Info for Admin/User Settings
	SystemRepoInfo      *gitutils.RepoInfo
	UserRepoInfo        *gitutils.RepoInfo
	GlobalSystemRepoURL string

	// App Config
	App         *data.App
	Schema      template.JS
	AppConfig   map[string]any
	AppMetadata *apps.AppMetadata

	// Device Update Extras
	ColorFilterOptions []ColorFilterOption
	AvailableLocales   []string
	DefaultImgURL      string
	DefaultWsURL       string
	FirmwareImgURL     string
	BrightnessUI       int
	NightBrightnessUI  int
	DimBrightnessUI    int

	// Firmware
	FirmwareBinsAvailable     bool
	FirmwareAvailable         bool
	FirmwareVersion           string
	AvailableFirmwareVersions []string
	ServerVersion             string
	CommitHash                string
	IsAutoLoginActive         bool   // Indicate if single-user auto-login is active
	UserCount                 int    // Number of users, for registration logic
	DeleteOnCancel            bool   // Indicate if app should be deleted on cancel
	URLWarning                string // Warning about localhost in image URL
	ReadOnly                  bool   // Indicate if the view should be read-only
	Partial                   string
}

// CreateDeviceFormData represents the form data for creating a device.
type CreateDeviceFormData struct {
	Name           string
	DeviceID       string
	DeviceIDMode   string
	DeviceType     string
	ImgURL         string
	WsURL          string
	APIKey         string
	RequireAPIKey  bool
	Notes          string
	Brightness     int
	LocationSearch string
	LocationJSON   string
}

func (s *Server) renderTemplate(w http.ResponseWriter, r *http.Request, name string, tmplData TemplateData) {
	if tmplData.Config == nil {
		tmplData.Config = &config.TemplateConfig{
			EnableUserRegistration: s.Config.EnableUserRegistration,
			SingleUserAutoLogin:    s.Config.SingleUserAutoLogin,
			SystemAppsAutoRefresh:  s.Config.SystemAppsAutoRefresh,
			Production:             s.Config.Production,
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
			user, err := gorm.G[data.User](s.DB).
				Preload("Devices", nil).
				Preload("Devices.Apps", nil).
				Where("username = ?", username).
				First(r.Context())
			if err == nil {
				tmplData.User = &user
			}
		}
	}

	// Calculate IsAutoLoginActive
	userCount, err := gorm.G[data.User](s.DB).Count(r.Context(), "*")
	if err != nil {
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

	// Render partial if requested
	if tmplData.Partial != "" {
		err = tmpl.ExecuteTemplate(w, tmplData.Partial, tmplData)
		if err != nil {
			slog.Error("Failed to render partial", "template", name, "partial", tmplData.Partial, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

	// Execute pre-parsed template
	err = tmpl.ExecuteTemplate(w, name, tmplData)
	if err != nil {
		slog.Error("Failed to render template", "template", name, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// localizeOrID helper to safely localize or return ID.
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

// getDeviceTypeChoices returns a slice of device type options with display names.
func (s *Server) getDeviceTypeChoices(localizer *i18n.Localizer) []DeviceTypeOption {
	allDeviceTypes := []data.DeviceType{
		data.DeviceTidbytGen1,
		data.DeviceTidbytGen2,
		data.DeviceTronbytS3,
		data.DeviceTronbytS3Wide,
		data.DeviceMatrixPortal,
		data.DeviceMatrixPortalWS,
		data.DevicePixoticker,
		data.DeviceRaspberryPi,
		data.DeviceRaspberryPiWide,
		data.DeviceOther,
	}

	choices := make([]DeviceTypeOption, 0, len(allDeviceTypes))
	for _, dt := range allDeviceTypes {
		choices = append(choices, DeviceTypeOption{
			Value: dt,
			Label: s.localizeOrID(localizer, dt.String()),
		})
	}
	return choices
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

// generateSecureToken generates a URL-safe, base64 encoded, securely random string.
// This is used for generating API keys and device IDs.
func generateSecureToken(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b)[:length], nil // Take only requested length
}

// flashAndRedirect adds a flash message and redirects to the specified URL.
func (s *Server) flashAndRedirect(w http.ResponseWriter, r *http.Request, messageID string, redirectURL string, status int) {
	localizer := s.getLocalizer(r)
	session, _ := s.Store.Get(r, "session-name")
	session.AddFlash(s.localizeOrID(localizer, messageID))
	if err := s.saveSession(w, r, session); err != nil {
		slog.Error("Failed to save session", "error", err)
	}
	http.Redirect(w, r, redirectURL, status)
}

// parseTimeInput parses time input in various formats and returns as HH:MM string.
func parseTimeInput(timeStr string) (string, error) {
	timeStr = strings.TrimSpace(timeStr)
	if timeStr == "" {
		return "", fmt.Errorf("time cannot be empty")
	}

	var hour, minute int
	var err error

	if strings.Contains(timeStr, ":") {
		parts := strings.Split(timeStr, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return "", fmt.Errorf("invalid time format: %s", timeStr)
		}
		hour, err = strconv.Atoi(parts[0])
		if err != nil {
			return "", fmt.Errorf("time must contain only numbers: %s", timeStr)
		}
		minute, err = strconv.Atoi(parts[1])
		if err != nil {
			return "", fmt.Errorf("time must contain only numbers: %s", timeStr)
		}
	} else {
		if len(timeStr) == 4 {
			hour, err = strconv.Atoi(timeStr[:2])
			if err != nil {
				return "", err
			}
			minute, err = strconv.Atoi(timeStr[2:])
			if err != nil {
				return "", err
			}
		} else if len(timeStr) == 3 {
			hour, err = strconv.Atoi(timeStr[:1])
			if err != nil {
				return "", err
			}
			minute, err = strconv.Atoi(timeStr[1:])
			if err != nil {
				return "", err
			}
		} else if len(timeStr) == 2 || len(timeStr) == 1 {
			hour, err = strconv.Atoi(timeStr)
			if err != nil {
				return "", err
			}
			minute = 0
		} else {
			return "", fmt.Errorf("invalid time format: %s", timeStr)
		}
	}

	if hour < 0 || hour > 23 {
		return "", fmt.Errorf("hour must be between 0 and 23: %d", hour)
	}
	if minute < 0 || minute > 59 {
		return "", fmt.Errorf("minute must be between 0 and 59: %d", minute)
	}

	return fmt.Sprintf("%02d:%02d", hour, minute), nil
}

func (s *Server) sanitizeURL(u string) string {
	return strings.TrimSpace(u)
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
		for proxy := range strings.SplitSeq(trustedProxies, ",") {
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

// saveSession saves the session with dynamic Secure flag based on request scheme.
func (s *Server) saveSession(w http.ResponseWriter, r *http.Request, session *sessions.Session) error {
	// Create a copy of options to modify safely
	opts := *session.Options
	opts.Secure = r.URL.Scheme == "https"
	session.Options = &opts

	return session.Save(r, w)
}

// ensureDeviceImageDir is a helper to get and ensure the device webp directory exists.
func (s *Server) ensureDeviceImageDir(deviceID string) (string, error) {
	path, err := securejoin.SecureJoin(filepath.Join(s.DataDir, "webp"), deviceID)
	if err != nil {
		return "", fmt.Errorf("failed to securejoin path for device webp directory %s: %w", deviceID, err)
	}

	if err := os.MkdirAll(path, 0755); err != nil {
		return "", fmt.Errorf("failed to create device webp directory %s: %w", path, err)
	}
	return path, nil
}

// ListSystemApps returns a thread-safe copy of the system apps cache.
func (s *Server) ListSystemApps() []apps.AppMetadata {
	s.systemAppsCacheMutex.RLock()
	defer s.systemAppsCacheMutex.RUnlock()

	list := make([]apps.AppMetadata, len(s.systemAppsCache))
	copy(list, s.systemAppsCache)
	return list
}

func (s *Server) RefreshSystemAppsCache() {
	s.systemAppsCacheMutex.Lock()
	defer s.systemAppsCacheMutex.Unlock()

	slog.Info("Refreshing system apps cache")
	apps, err := apps.ListSystemApps(s.DataDir)
	if err == nil {
		s.systemAppsCache = apps
		slog.Info("System apps cache refreshed", "count", len(s.systemAppsCache))
	} else {
		slog.Error("Failed to refresh system apps cache", "error", err)
	}
}

// getAppMetadata retrieves metadata for an app path, checking the system cache first,
// then falling back to reading the manifest.yaml from disk.
func (s *Server) getAppMetadata(appPath string) *apps.AppMetadata {
	if appPath == "" {
		return nil
	}

	var appMetadata *apps.AppMetadata
	appDir := filepath.ToSlash(filepath.Dir(appPath))

	s.systemAppsCacheMutex.RLock()
	for i := range s.systemAppsCache {
		metaPath := s.systemAppsCache[i].Path
		// Check for exact match (if appPath is the directory) or parent directory match (if appPath is a file)
		if appPath == metaPath || appDir == metaPath {
			meta := s.systemAppsCache[i]
			appMetadata = &meta
			break
		}
	}
	s.systemAppsCacheMutex.RUnlock()

	if appMetadata != nil {
		return appMetadata
	}

	// Fallback: Check for manifest.yaml in app directory
	fullPath, err := securejoin.SecureJoin(s.DataDir, appPath)
	if err == nil {
		var appDir string
		info, err := os.Stat(fullPath)
		if err == nil && info.IsDir() {
			appDir = fullPath
		} else {
			appDir = filepath.ToSlash(filepath.Dir(fullPath))
		}

		manifestPath := filepath.Join(appDir, "manifest.yaml")
		if _, err := os.Stat(manifestPath); err == nil {
			if data, err := os.ReadFile(manifestPath); err == nil {
				var m apps.Manifest
				if err := yaml.Unmarshal(data, &m); err == nil {
					return &apps.AppMetadata{
						Manifest: m,
					}
				} else {
					slog.Debug("Failed to unmarshal manifest for fallback", "path", manifestPath, "error", err)
				}
			}
		}
	}

	return nil
}

func (s *Server) getSetting(key string) (string, error) {
	setting, err := gorm.G[data.Setting](s.DB).Where("key = ?", key).First(context.Background())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return setting.Value, nil
}

func (s *Server) setSetting(key, value string) error {
	setting := data.Setting{Key: key, Value: value}
	return gorm.G[data.Setting](s.DB, clause.OnConflict{UpdateAll: true}).Create(context.Background(), &setting)
}

func (s *Server) notifyDashboard(username string, event WSEvent) {
	data, err := json.Marshal(event)
	if err != nil {
		slog.Error("Failed to marshal WS event", "error", err)
		return
	}
	s.Broadcaster.Notify("user:"+username, data)

	if event.DeviceID != "" {
		// Signal device loop to reload (empty message triggers DB reload in wsWriteLoop)
		s.Broadcaster.Notify(event.DeviceID, nil)
	}
}

func (s *Server) GetBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" {
		host = r.Host
	}

	if port := r.Header.Get("X-Forwarded-Port"); port != "" {
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = net.JoinHostPort(host, port)
	}

	return fmt.Sprintf("%s://%s", scheme, host)
}

func (s *Server) getImageURL(r *http.Request, deviceID string) string {
	baseURL := s.GetBaseURL(r)
	return fmt.Sprintf("%s/%s/next", baseURL, deviceID)
}

func (s *Server) getImageURLWithKey(r *http.Request, deviceID string, apiKey string) string {
	u := s.getImageURL(r, deviceID)
	if apiKey != "" {
		u += "?key=" + apiKey
	}
	return u
}

// appendKeyToURLString appends ?key=apiKey (or &key=apiKey) to a URL string.
// It first removes any existing key parameter to avoid double-append.
func appendKeyToURLString(rawURL string, apiKey string) string {
	if apiKey == "" || rawURL == "" {
		return rawURL
	}

	// Remove existing key parameter first to avoid double-append
	rawURL = removeKeyFromURLString(rawURL)

	if strings.Contains(rawURL, "?") {
		return rawURL + "&key=" + apiKey
	}
	return rawURL + "?key=" + apiKey
}

// removeKeyFromURLString removes the key query parameter from a URL string.
func removeKeyFromURLString(rawURL string) string {
	if rawURL == "" {
		return rawURL
	}

	// Simple regex-free approach
	u, err := url.Parse(rawURL)
	if err != nil {
		// If parsing fails, do simple string replacement
		re := regexp.MustCompile(`([?&])key=[^&]*(&|$)`)
		result := re.ReplaceAllString(rawURL, "$1")
		result = strings.TrimSuffix(result, "?")
		result = strings.TrimSuffix(result, "&")
		return result
	}

	query := u.Query()
	query.Del("key")
	u.RawQuery = query.Encode()
	return u.String()
}

// extractDeviceKey extracts the device API key from the request.
// It checks the "key" query parameter first, then the Authorization: Bearer header.
func extractDeviceKey(r *http.Request) string {
	if key := r.URL.Query().Get("key"); key != "" {
		return key
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

func (s *Server) getWebsocketURL(r *http.Request, deviceID string) string {
	baseURL := s.GetBaseURL(r)
	wsScheme := "ws"
	if strings.HasPrefix(baseURL, "https") {
		wsScheme = "wss"
	}
	return fmt.Sprintf("%s://%s/%s/ws", wsScheme, r.Host, deviceID)
}

func (s *Server) getWebsocketURLWithKey(r *http.Request, deviceID string, apiKey string) string {
	u := s.getWebsocketURL(r, deviceID)
	if apiKey != "" {
		u += "?key=" + apiKey
	}
	return u
}

func stringPtr(s string) *string {
	return &s
}

func intPtr(i int) *int {
	return &i
}

func boolPtr(b bool) *bool {
	return &b
}

var rebootPayloadJSON = []byte(`{"reboot":true}`)

func (s *Server) sendRebootCommand(deviceID string) error {
	s.Broadcaster.Notify(deviceID, DeviceCommandMessage{Payload: rebootPayloadJSON})
	return nil
}

// orderedAppsPreload defines a GORM preload function to sort associated apps by their 'order' field.
var orderedAppsPreload = func(db gorm.PreloadBuilder) error {
	db.Order(clause.OrderByColumn{Column: clause.Column{Name: "order"}, Desc: false})
	return nil
}
