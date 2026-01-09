package server

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"maps"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/gitutils"
	"tronbyt-server/internal/renderer"

	securejoin "github.com/cyphar/filepath-securejoin"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AppManifest reflects a subset of manifest.yaml for internal updates.
type AppManifest struct {
	Broken       *bool   `yaml:"broken,omitempty"`
	BrokenReason *string `yaml:"brokenReason,omitempty"`
}

var packageNameRegex = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func (s *Server) handleAddAppGet(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

	systemApps := s.ListSystemApps()
	customApps := apps.ListUserApps(s.DataDir, user.Username)

	s.markInstalledApps(device, systemApps, customApps)

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

func (s *Server) markInstalledApps(device *data.Device, systemApps []apps.AppMetadata, customApps []apps.AppMetadata) {
	// Mark installed apps
	installedNames := make(map[string]bool)
	installedPaths := make(map[string]bool)
	for _, da := range device.Apps {
		installedNames[da.Name] = true
		if da.Path != nil && *da.Path != "" {
			p := *da.Path
			installedPaths[p] = true
			installedPaths[filepath.Dir(p)] = true

			// Also track relative path to ensure matching works if DB has absolute paths
			// but ListSystemApps returns relative paths.
			if rel, err := filepath.Rel(s.DataDir, p); err == nil {
				installedPaths[rel] = true
				installedPaths[filepath.Dir(rel)] = true
			}
		}
	}

	for i := range systemApps {
		fullPath := filepath.Join(systemApps[i].Path, systemApps[i].FileName)
		if installedNames[systemApps[i].ID] || installedNames[systemApps[i].Name] ||
			installedPaths[systemApps[i].Path] || installedPaths[fullPath] {
			systemApps[i].IsInstalled = true
		}
	}
	for i := range customApps {
		if installedNames[customApps[i].ID] || installedNames[customApps[i].Name] ||
			installedPaths[customApps[i].Path] || installedPaths[filepath.Dir(customApps[i].Path)] {
			customApps[i].IsInstalled = true
		}
	}
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

	// 1. Find App Details (Recommended Interval)
	recommendedInterval := 15 // Default

	// Try to get metadata using getAppMetadata (checks cache and disk)
	if metadata := s.getAppMetadata(appPath); metadata != nil {
		recommendedInterval = metadata.RecommendedInterval
	}

	uinterval := 0
	if uintervalStr != "" {
		if val, err := strconv.Atoi(uintervalStr); err == nil {
			uinterval = val
		}
	}

	// Use recommended_interval logic
	if uinterval == 0 || (uinterval == 10 && recommendedInterval != 10) {
		uinterval = recommendedInterval
	}
	if uinterval == 0 {
		uinterval = 10
	}

	displayTime, _ := strconv.Atoi(displayTimeStr)

	// Construct source path
	realPath, err := securejoin.SecureJoin(s.DataDir, appPath)
	if err != nil {
		slog.Warn("Potential path traversal", "path", appPath, "error", err)
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	iname, err := generateUniqueIname(s.DB, device.ID)
	if err != nil {
		slog.Error("Failed to generate unique iname", "error", err)
		http.Error(w, "Could not generate unique iname", http.StatusInternalServerError)
		return
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

	maxOrder, err := getMaxAppOrder(s.DB, device.ID)
	if err != nil {
		slog.Error("Failed to get max app order", "error", err)
	}
	newApp.Order = maxOrder + 1

	if err := gorm.G[data.App](s.DB).Create(r.Context(), &newApp); err != nil {
		slog.Error("Failed to save app", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// If WebP, copy file and redirect to index
	isWebP := strings.EqualFold(filepath.Ext(appPath), ".webp")
	if isWebP {
		destDir, err := s.ensureDeviceImageDir(device.ID)
		if err != nil {
			slog.Error("Failed to get device webp directory for app copy", "device_id", device.ID, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		destPath, err := securejoin.SecureJoin(destDir, fmt.Sprintf("%s-%s.webp", newApp.Name, newApp.Iname))
		if err != nil {
			slog.Warn("Path traversal attempt blocked", "error", err)
			http.Error(w, "Invalid filename", http.StatusBadRequest)
			return
		}

		srcFile, err := os.Open(realPath)
		if err == nil {
			defer func() {
				if err := srcFile.Close(); err != nil {
					slog.Error("Failed to close source webp", "error", err)
				}
			}()
			destFile, err := os.Create(destPath)
			if err == nil {
				defer func() {
					if err := destFile.Close(); err != nil {
						slog.Error("Failed to close destination webp", "error", err)
					}
				}()
				if _, err := io.Copy(destFile, srcFile); err != nil {
					slog.Error("Failed to copy webp content", "error", err)
				}
			} else {
				slog.Error("Failed to create destination webp", "error", err)
			}
		} else {
			slog.Error("Failed to open source webp", "error", err)
		}

		s.possiblyRender(r.Context(), &newApp, device, user)

		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Trigger initial render
	s.possiblyRender(r.Context(), &newApp, device, user)

	// Notify Dashboard & Device
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	http.Redirect(w, r, fmt.Sprintf("/devices/%s/%s/config?delete_on_cancel=true", device.ID, newApp.Iname), http.StatusSeeOther)
}

func (s *Server) handleAppThumbnail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "App ID required", http.StatusBadRequest)
		return
	}

	user, _ := UserFromContext(r.Context())

	var appMeta *apps.AppMetadata

	// 1. Check system apps cache
	s.systemAppsCacheMutex.RLock()
	for i := range s.systemAppsCache {
		if s.systemAppsCache[i].ID == id {
			meta := s.systemAppsCache[i]
			appMeta = &meta
			break
		}
	}
	s.systemAppsCacheMutex.RUnlock()

	// 2. If not found in system apps, check user apps (if logged in)
	if appMeta == nil && user != nil {
		userApps := apps.ListUserApps(s.DataDir, user.Username)
		for i := range userApps {
			if userApps[i].ID == id {
				meta := userApps[i]
				appMeta = &meta
				break
			}
		}
	}

	if appMeta == nil {
		http.NotFound(w, r)
		return
	}

	// 3. Determine file to serve
	// Pick the best preview from metadata.
	var file string
	if appMeta.Supports2x && appMeta.Preview2x != "" {
		file = appMeta.Preview2x
	} else {
		file = appMeta.Preview
	}

	if file == "" {
		http.NotFound(w, r)
		return
	}

	// 4. Resolve full path
	// apps.AppMetadata.Path is relative to DataDir
	// For System Apps: Path=".../clock", Dir(Path)=".../apps", file="clock/preview.webp" -> Join=".../apps/clock/preview.webp" (Correct)
	// For User Apps: Path=".../app.star", Dir(Path)=".../app", file="preview.webp" -> Join=".../app/preview.webp" (Correct)
	appDir := filepath.Join(s.DataDir, filepath.Dir(appMeta.Path))

	path, err := securejoin.SecureJoin(appDir, file)
	if err != nil {
		slog.Warn("Invalid thumbnail path", "error", err, "path", file)
		http.NotFound(w, r)
		return
	}

	http.ServeFile(w, r, path)
}

func (s *Server) handleConfigAppGet(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)
	app := GetApp(r)

	// Get Schema
	var schemaBytes []byte
	if app.Path != nil && *app.Path != "" {
		appPath, err := securejoin.SecureJoin(s.DataDir, *app.Path)
		if err != nil {
			slog.Error("Failed to resolve app path", "path", *app.Path, "error", err)
			http.Error(w, "Invalid app path", http.StatusBadRequest)
			return
		} else {
			if !strings.HasSuffix(strings.ToLower(appPath), ".webp") {
				schemaBytes, err = renderer.GetSchema(appPath, 64, 32, device.Type.Supports2x())
				if err != nil {
					slog.Error("Failed to get app schema", "error", err)
					// Fall through with empty schema
				}
			}
		}
	}
	if len(schemaBytes) == 0 {
		schemaBytes = []byte("{}")
	}

	deleteOnCancel := r.URL.Query().Get("delete_on_cancel") == "true"

	var appMetadata *apps.AppMetadata
	if app.Path != nil {
		appMetadata = s.getAppMetadata(*app.Path)
	}

	s.renderTemplate(w, r, "configapp", TemplateData{
		User:               user,
		Device:             device,
		App:                app,
		Schema:             template.JS(schemaBytes),
		AppConfig:          app.Config,
		DeleteOnCancel:     deleteOnCancel,
		ColorFilterOptions: s.getColorFilterChoices(),
		AppMetadata:        appMetadata,
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

		StartTime string   `json:"start_time"`
		EndTime   string   `json:"end_time"`
		Days      []string `json:"days"`

		RecurrenceType      data.RecurrenceType `json:"recurrence_type"`
		RecurrenceInterval  int                 `json:"recurrence_interval"`
		RecurrencePattern   map[string]any      `json:"recurrence_pattern"`
		RecurrenceStartDate string              `json:"recurrence_start_date"`
		RecurrenceEndDate   string              `json:"recurrence_end_date"`
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
	app.UseCustomRecurrence = payload.UseCustomRecurrence
	app.RecurrenceType = payload.RecurrenceType
	app.RecurrenceInterval = payload.RecurrenceInterval
	app.RecurrencePattern = payload.RecurrencePattern

	// Handle optional string pointers
	if payload.StartTime != "" {
		parsed, err := parseTimeInput(payload.StartTime)
		if err != nil {
			slog.Warn("Invalid start time", "time", payload.StartTime, "error", err)
			http.Error(w, fmt.Sprintf("Invalid start time: %v", err), http.StatusBadRequest)
			return
		}
		app.StartTime = &parsed
	} else {
		app.StartTime = nil
	}
	if payload.EndTime != "" {
		parsed, err := parseTimeInput(payload.EndTime)
		if err != nil {
			slog.Warn("Invalid end time", "time", payload.EndTime, "error", err)
			http.Error(w, fmt.Sprintf("Invalid end time: %v", err), http.StatusBadRequest)
			return
		}
		app.EndTime = &parsed
	} else {
		app.EndTime = nil
	}
	if payload.RecurrenceStartDate != "" {
		app.RecurrenceStartDate = &payload.RecurrenceStartDate
	} else {
		app.RecurrenceStartDate = nil
	}
	if payload.RecurrenceEndDate != "" {
		app.RecurrenceEndDate = &payload.RecurrenceEndDate
	} else {
		app.RecurrenceEndDate = nil
	}

	// Handle Days slice
	app.Days = payload.Days

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
	// Force render by resetting LastRender
	app.LastRender = time.Time{}
	s.possiblyRender(r.Context(), app, device, user)

	// Notify Dashboard & Device
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

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

	// Read Script
	if app.Path == nil || *app.Path == "" {
		http.Error(w, "App path not set", http.StatusBadRequest)
		return
	}
	appPath, err := securejoin.SecureJoin(s.DataDir, *app.Path)
	if err != nil {
		slog.Error("Failed to resolve app path", "path", *app.Path, "error", err)
		http.Error(w, "Invalid app path", http.StatusBadRequest)
		return
	}

	// Call Handler
	result, err := renderer.CallSchemaHandler(
		r.Context(),
		appPath,
		payload.Config,
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
	// The device and user are already in context due to RequireLogin and RequireDevice
	device := GetDevice(r)

	imgData, _, err := s.GetCurrentAppImage(r.Context(), device)
	if err != nil {
		s.sendDefaultImage(w, r, device)
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

		if app.Path == nil || *app.Path == "" {
			http.Error(w, "App path not set", http.StatusBadRequest)
			return
		}
		appPath, err := securejoin.SecureJoin(s.DataDir, *app.Path)
		if err != nil {
			slog.Error("Failed to resolve app path", "path", *app.Path, "error", err)
			http.Error(w, "Invalid app path", http.StatusBadRequest)
			return
		}

		imgBytes, _, err := s.RenderApp(r.Context(), device, app, appPath, configData)
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
	webpDir, err := s.ensureDeviceImageDir(id)
	if err != nil {
		slog.Error("Failed to get device webp directory for config preview", "device_id", id, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
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

	// Render
	if app.Path == nil || *app.Path == "" {
		http.Error(w, "App path not set", http.StatusBadRequest)
		return
	}
	appPath, err := securejoin.SecureJoin(s.DataDir, *app.Path)
	if err != nil {
		slog.Error("Failed to resolve app path", "path", *app.Path, "error", err)
		http.Error(w, "Invalid app path", http.StatusBadRequest)
		return
	}

	imgBytes, messages, err := s.RenderApp(r.Context(), device, app, appPath, configBody)
	if err != nil {
		slog.Error("Preview render failed", "error", err)
		http.Error(w, "Render failed", http.StatusInternalServerError)
		return
	}
	for _, msg := range messages {
		slog.Debug("Preview render message", "message", msg)
	}

	// Push preview image to device (ephemeral)
	if err := s.savePushedImage(device.ID, app.Iname, imgBytes); err != nil {
		http.Error(w, "Failed to push preview", http.StatusInternalServerError)
		return
	}

	// Notify device via Websocket (Broadcaster)
	s.Broadcaster.Notify(device.ID, imgBytes)

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)
	app := GetApp(r)

	err := s.DB.Transaction(func(tx *gorm.DB) error {
		// Unpin if pinned
		if device.PinnedApp != nil && *device.PinnedApp == app.Iname {
			if _, err := gorm.G[data.Device](tx).Where("id = ?", device.ID).Update(r.Context(), "pinned_app", nil); err != nil {
				return err
			}
		}

		// Unset Night Mode App if it matches
		if device.NightModeApp == app.Iname {
			if _, err := gorm.G[data.Device](tx).Where("id = ?", device.ID).Update(r.Context(), "night_mode_app", ""); err != nil {
				return err
			}
		}

		// Delete App
		if _, err := gorm.G[data.App](tx).Where("id = ?", app.ID).Delete(r.Context()); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		slog.Error("Failed to delete app", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Clean up webp (outside transaction, as file system ops can't be rolled back easily)
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	if err != nil {
		slog.Error("Failed to get device webp directory for app delete cleanup", "device_id", device.ID, "error", err)
		// Don't error out the request if file cleanup fails, but log it
	} else {
		matches, _ := filepath.Glob(filepath.Join(webpDir, fmt.Sprintf("*-%s.webp", app.Iname)))
		for _, match := range matches {
			if err := os.Remove(match); err != nil {
				slog.Error("Failed to remove app webp file", "path", match, "error", err)
			}
		}
	}

	// Notify Dashboard
	// We need the user to notify. GetUser(r) gets it from context.
	user := GetUser(r)
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

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
		if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(r.Context(), "pinned_app", nil); err != nil {
			slog.Error("Failed to unpin app", "error", err)
		}
	} else {
		if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(r.Context(), "pinned_app", newPinned); err != nil {
			slog.Error("Failed to pin app", "error", err)
		}
	}

	// Notify Dashboard
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleToggleEnabled(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	app := GetApp(r)

	app.Enabled = !app.Enabled
	if _, err := gorm.G[data.App](s.DB).Where("id = ?", app.ID).Update(r.Context(), "enabled", app.Enabled); err != nil {
		slog.Error("Failed to toggle enabled", "error", err)
		http.Error(w, "Failed to toggle enabled status", http.StatusInternalServerError)
		return
	}

	// Notify Dashboard
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: app.DeviceID})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleMoveApp(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)
	targetApp := GetApp(r)
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
				if _, err := gorm.G[data.App](tx).Where("id = ?", appsList[i].ID).Update(r.Context(), "order", i); err != nil {
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
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDuplicateApp(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)
	originalApp := GetApp(r)

	// insertAfter is implicitly true for simple duplication
	if err := s.performAppDuplication(r.Context(), user, device, originalApp, device, originalApp.Iname, true); err != nil {
		slog.Error("Failed to duplicate app", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Notify Dashboard
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDuplicateAppToDevice(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

	sourceDeviceID := r.PathValue("source_device_id")
	targetDeviceID := r.PathValue("id")
	iname := r.PathValue("iname")

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

	if err := s.performAppDuplication(r.Context(), user, sourceDevice, originalApp, targetDevice, targetIname, insertAfter); err != nil {
		slog.Error("Failed to duplicate app", "error", err)
		http.Error(w, fmt.Sprintf("Failed to duplicate app: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("OK")); err != nil {
		slog.Error("Failed to write OK response", "error", err)
	}
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

	app := appsList[draggedIdx]
	appsList = append(appsList[:draggedIdx], appsList[draggedIdx+1:]...)

	if draggedIdx < targetIdx {
		targetIdx--
	}

	if insertAfter {
		targetIdx++
	}

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
				if _, err := gorm.G[data.App](tx).Where("id = ?", appsList[i].ID).Update(r.Context(), "order", i); err != nil {
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
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleUploadAppGet(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

	s.renderTemplate(w, r, "uploadapp", TemplateData{User: user, Device: device})
}

func (s *Server) handleUploadAppPost(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)

	if err := r.ParseMultipartForm(10 << 20); err != nil {
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

	filename := filepath.Base(header.Filename)
	ext := filepath.Ext(filename)
	if ext != ".star" && ext != ".webp" && ext != ".zip" {
		http.Error(w, "Invalid file type", http.StatusBadRequest)
		return
	}

	appName := strings.TrimSuffix(filename, ext)

	userAppsDir := filepath.Join(s.DataDir, "users", user.Username, "apps")
	appDir, err := securejoin.SecureJoin(userAppsDir, appName)
	if err != nil {
		slog.Warn("Path traversal attempt blocked", "error", err)
		http.Error(w, "Invalid app name", http.StatusBadRequest)
		return
	}
	previewDir := appDir

	// Handle zip files specifically
	if ext == ".zip" {
		if err := s.handleZipUpload(w, r, user, device, file, header, appName); err != nil {
			slog.Error("Failed to handle zip upload", "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		}
		return
	}

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
	_ = dst.Sync()

	switch ext {
	case ".star":
		imgBytes, _, err := s.RenderApp(r.Context(), device, nil, dstPath, nil)

		if err == nil && len(imgBytes) > 0 {
			previewPath := filepath.Join(previewDir, fmt.Sprintf("%s.webp", appName))
			if err := os.WriteFile(previewPath, imgBytes, 0644); err != nil {
				slog.Error("Failed to write preview image", "error", err)
			}
		} else {
			slog.Error("Failed to render preview", "error", err)
		}
	}

	http.Redirect(w, r, fmt.Sprintf("/devices/%s/addapp", device.ID), http.StatusSeeOther)
}

func (s *Server) parseManifest(tempExtractDir string) (string, error) {
	manifestPath := filepath.Join(tempExtractDir, "manifest.yaml")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("Failed to read manifest.yaml", "error", err)
		}
		return "", nil // No manifest or read failed, continue with default name
	}

	var m apps.Manifest
	if err := yaml.Unmarshal(data, &m); err != nil {
		slog.Warn("Failed to parse manifest.yaml", "error", err)
		return "", nil
	}

	if m.PackageName != "" && packageNameRegex.MatchString(m.PackageName) {
		return m.PackageName, nil
	}
	return "", nil
}

func (s *Server) handleZipUpload(w http.ResponseWriter, r *http.Request, user *data.User, device *data.Device, file io.Reader, header *multipart.FileHeader, appName string) error {
	userAppsDir := filepath.Join(s.DataDir, "users", user.Username, "apps")

	// Save the zip file temporarily
	tempZip, err := os.CreateTemp("", "upload-*.zip")
	if err != nil {
		slog.Error("Failed to create temp zip file", "error", err)
		return err
	}
	defer func() {
		if err := os.Remove(tempZip.Name()); err != nil {
			slog.Error("Failed to remove temp zip file", "error", err)
		}
	}()

	if _, err := io.Copy(tempZip, file); err != nil {
		slog.Error("Failed to write temp zip file", "error", err)
		if cerr := tempZip.Close(); cerr != nil {
			slog.Error("Failed to close temp zip file after error", "error", cerr)
		}
		return err
	}
	if err := tempZip.Close(); err != nil {
		slog.Error("Failed to close temp zip file", "error", err)
		return err
	}

	// Create a temp dir for extraction
	tempExtractDir, err := os.MkdirTemp("", "app-extract-*")
	if err != nil {
		slog.Error("Failed to create temp extract dir", "error", err)
		return err
	}
	defer func() {
		if err := os.RemoveAll(tempExtractDir); err != nil {
			slog.Error("Failed to remove temp extract dir", "error", err)
		}
	}()

	// Unzip contents to temp dir
	if err := s.unzip(tempZip.Name(), tempExtractDir); err != nil {
		slog.Error("Failed to unzip file", "error", err)
		return err
	}

	if parsedName, _ := s.parseManifest(tempExtractDir); parsedName != "" {
		appName = parsedName
	}

	// Re-calculate appDir with potentially new appName
	appDir, err := securejoin.SecureJoin(userAppsDir, appName)
	if err != nil {
		slog.Warn("Path traversal attempt blocked", "error", err)
		return err
	}

	// Replace existing directory
	if err := os.RemoveAll(appDir); err != nil {
		slog.Error("Failed to remove existing app dir", "path", appDir, "error", err)
		return err
	}
	if err := os.MkdirAll(appDir, 0755); err != nil {
		slog.Error("Failed to create app dir", "error", err)
		return err
	}

	// Copy files from tempExtractDir to appDir
	if err := copyDir(tempExtractDir, appDir); err != nil {
		slog.Error("Failed to copy extracted files", "error", err)
		return err
	}

	s.generatePreview(r.Context(), device, appDir, appDir, appName)

	http.Redirect(w, r, fmt.Sprintf("/devices/%s/addapp", device.ID), http.StatusSeeOther)
	return nil
}

func (s *Server) generatePreview(ctx context.Context, device *data.Device, appDir, previewDir, appName string) {
	// Check for a .webp preview image to copy
	var webpPath string
	entries, err := os.ReadDir(appDir)
	if err != nil {
		slog.Error("Failed to read app dir for preview generation", "path", appDir, "error", err)
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			if strings.HasSuffix(entry.Name(), ".webp") {
				if webpPath == "" || entry.Name() == appName+".webp" {
					webpPath = filepath.Join(appDir, entry.Name())
				}
			}
		}
	}

	previewPath := filepath.Join(previewDir, fmt.Sprintf("%s.webp", appName))
	if webpPath != "" {
		// If a webp exists, copy it to preview
		// Note: The UI expects [AppName].webp in the previews dir
		if webpPath != previewPath {
			if err := copyFile(webpPath, previewPath); err != nil {
				slog.Error("Failed to copy preview image from zip", "error", err)
			}
		}
	} else {
		// If no webp, try to render the app directory
		imgBytes, _, err := s.RenderApp(ctx, device, nil, appDir, nil)
		if err == nil && len(imgBytes) > 0 {
			if err := os.WriteFile(previewPath, imgBytes, 0644); err != nil {
				slog.Error("Failed to write preview image", "error", err)
			}
		} else {
			slog.Error("Failed to render preview", "error", err)
		}
	}
}

func copyDir(src string, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		if info.Mode()&os.ModeSymlink != 0 {
			// Skip symlinks for safety
			return nil
		}

		return copyFile(path, dstPath)
	})
}

func (s *Server) unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer func() {
		if err := r.Close(); err != nil {
			slog.Error("Failed to close zip reader", "error", err)
		}
	}()
	for _, f := range r.File {
		// securejoin to prevent zip slip
		fpath, err := securejoin.SecureJoin(dest, f.Name)
		if err != nil {
			return err
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(fpath, f.Mode()); err != nil {
				return err
			}
			continue
		}

		if err = os.MkdirAll(filepath.Dir(fpath), 0755); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			// Ensure outFile is closed if rc.Open() fails
			if closeErr := outFile.Close(); closeErr != nil {
				slog.Error("Failed to close extracted file after rc.Open() failed", "error", closeErr)
			}
			return err
		}

		_, err = io.Copy(outFile, rc)
		copyErr := err // Preserve copy error

		// Attempt to close both files, logging errors if they occur
		outFileCloseErr := outFile.Close()
		rcCloseErr := rc.Close()

		// Prioritize copy error
		if copyErr != nil {
			if outFileCloseErr != nil {
				slog.Error("Failed to close extracted file after copy error", "error", outFileCloseErr)
			}
			if rcCloseErr != nil {
				slog.Error("Failed to close zip file reader after copy error", "error", rcCloseErr)
			}
			return copyErr
		}

		// If copy was successful, return close errors if any
		if outFileCloseErr != nil {
			return outFileCloseErr
		}
		if rcCloseErr != nil {
			return rcCloseErr
		}
	}
	return nil
}

func (s *Server) handleDeleteUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	filename := filepath.Base(r.PathValue("filename"))

	user := GetUser(r)

	// user.Devices is already preloaded by RequireLogin -> RequireDevice -> RequireUser, but GetUser in current implementation doesn't preload.
	// We need to fetch user with devices and apps here.
	userWithDevices, err := gorm.G[data.User](s.DB).Preload("Devices.Apps", nil).Where("username = ?", user.Username).First(r.Context())
	if err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	inUse := false
	for _, dev := range userWithDevices.Devices {
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
		s.flashAndRedirect(w, r, fmt.Sprintf("Cannot delete %s because it is installed on a device.", filename), fmt.Sprintf("/devices/%s/addapp", id), http.StatusSeeOther)
		return
	}

	userAppsPath := filepath.Join(s.DataDir, "users", userWithDevices.Username, "apps")
	appName := strings.TrimSuffix(filename, filepath.Ext(filename))
	appDir, err := securejoin.SecureJoin(userAppsPath, appName)
	if err != nil {
		slog.Warn("Path traversal attempt blocked", "error", err)
		http.Error(w, "Invalid path", http.StatusBadRequest)
		return
	}

	if err := os.RemoveAll(appDir); err != nil {
		slog.Error("Failed to remove app upload dir", "path", appDir, "error", err)
	}

	http.Redirect(w, r, fmt.Sprintf("/devices/%s/addapp", id), http.StatusSeeOther)
}

func (s *Server) handleMarkAppBroken(w http.ResponseWriter, r *http.Request) {
	s.updateAppBrokenStatus(w, r, true)
}

func (s *Server) handleUnmarkAppBroken(w http.ResponseWriter, r *http.Request) {
	s.updateAppBrokenStatus(w, r, false)
}

func (s *Server) updateAppBrokenStatus(w http.ResponseWriter, r *http.Request, broken bool) {
	if s.Config.Production == "1" {
		http.Error(w, "Not allowed in production mode", http.StatusForbidden)
		return
	}

	appName := r.URL.Query().Get("app_name")
	packageName := r.URL.Query().Get("package_name")

	if appName == "" {
		http.Error(w, "App name is required", http.StatusBadRequest)
		return
	}

	appID := appName
	if packageName != "" && packageName != "None" {
		appID = packageName
	}

	appsDir := filepath.Join(s.DataDir, "system-apps", "apps")
	manifestPath, err := securejoin.SecureJoin(appsDir, filepath.Join(appID, "manifest.yaml"))
	if err != nil {
		slog.Warn("Path traversal attempt blocked", "error", err)
		http.Error(w, "Invalid app ID", http.StatusBadRequest)
		return
	}

	var manifest AppManifest
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

	manifest.Broken = &broken
	if broken {
		reason := r.URL.Query().Get("broken_reason")
		if reason == "" {
			reason = "Marked broken by user"
		}
		manifest.BrokenReason = &reason
	} else {
		emptyReason := ""
		manifest.BrokenReason = &emptyReason
	}

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

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]bool{"success": true}); err != nil {
		slog.Error("Failed to write JSON success response", "error", err)
	}
}

func (s *Server) performAppDuplication(ctx context.Context, user *data.User, sourceDevice *data.Device, originalApp *data.App, targetDevice *data.Device, targetAppIname string, insertAfter bool) error {
	newIname, err := generateUniqueIname(s.DB, targetDevice.ID)
	if err != nil {
		return fmt.Errorf("failed to generate unique iname: %w", err)
	}

	duplicatedApp := *originalApp
	duplicatedApp.ID = 0
	duplicatedApp.DeviceID = targetDevice.ID
	duplicatedApp.Iname = newIname
	duplicatedApp.LastRender = time.Time{}
	duplicatedApp.Order = 0 // Will be set later
	duplicatedApp.AutoPin = false

	if originalApp.Config != nil {
		newConfig := make(data.JSONMap)
		maps.Copy(newConfig, originalApp.Config)
		duplicatedApp.Config = newConfig
	}

	// Pushed Apps: We don't have a source file, but we have a pushed image.
	// Non-Pushed Apps: We reference the existing path (no copy).
	if !originalApp.Pushed && originalApp.Path != nil && *originalApp.Path != "" {
		duplicatedApp.Pushed = false
		pathCopy := *originalApp.Path
		duplicatedApp.Path = &pathCopy
	}

	// Determine new order and insert into DB using transaction
	err = s.DB.Transaction(func(tx *gorm.DB) error {
		// Fetch current apps for target device to ensure up-to-date order
		currentApps, err := gorm.G[data.App](tx).Where("device_id = ?", targetDevice.ID).Order(clause.OrderByColumn{Column: clause.Column{Name: "order"}, Desc: false}).Find(ctx)
		if err != nil {
			return err
		}

		// Calculate insertion order
		insertOrder := 0
		if len(currentApps) > 0 {
			insertOrder = currentApps[len(currentApps)-1].Order + 1 // Default append
		}

		if targetAppIname != "" {
			for _, app := range currentApps {
				if app.Iname == targetAppIname {
					if insertAfter {
						insertOrder = app.Order + 1
					} else {
						insertOrder = app.Order
					}
					break
				}
			}
		}

		duplicatedApp.Order = insertOrder
		if err := gorm.G[data.App](tx).Create(ctx, &duplicatedApp); err != nil {
			return err
		}

		// Efficient SQL update for subsequent apps
		if _, err := gorm.G[data.App](tx).
			Where("device_id = ? AND id != ?", targetDevice.ID, duplicatedApp.ID).
			Where(gorm.Expr("? >= ?", clause.Column{Name: "order"}, insertOrder)).
			Update(ctx, "order", gorm.Expr("? + ?", clause.Column{Name: "order"}, 1)); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to save duplicated app: %w", err)
	}

	// Reload app to ensure ID is set
	reloaded, err := gorm.G[data.App](s.DB).Where("device_id = ? AND iname = ?", targetDevice.ID, newIname).First(ctx)
	if err != nil {
		return fmt.Errorf("failed to reload duplicated app: %w", err)
	}
	duplicatedApp = reloaded

	// Pushed App Image Copying
	if originalApp.Pushed {
		sourceWebpDir, err := s.ensureDeviceImageDir(sourceDevice.ID)
		if err != nil {
			slog.Error("Failed to get source device webp directory for duplication", "error", err)
		} else {
			targetWebpDir, err := s.ensureDeviceImageDir(targetDevice.ID)
			if err != nil {
				slog.Error("Failed to get target device webp directory for duplication", "error", err)
			} else {
				sourceWebpPath := filepath.Join(sourceWebpDir, "pushed", fmt.Sprintf("%s.webp", originalApp.Iname))
				targetPushedWebpDir := filepath.Join(targetWebpDir, "pushed")
				if err := os.MkdirAll(targetPushedWebpDir, 0755); err != nil {
					slog.Error("Failed to create target pushed webp directory", "path", targetPushedWebpDir, "error", err)
				}
				targetWebpPath := filepath.Join(targetPushedWebpDir, fmt.Sprintf("%s.webp", newIname))

				if _, err := os.Stat(sourceWebpPath); err == nil {
					if err := copyFile(sourceWebpPath, targetWebpPath); err != nil {
						slog.Error("Failed to copy pushed image", "source", sourceWebpPath, "target", targetWebpPath, "error", err)
					}
				} else if !os.IsNotExist(err) {
					slog.Error("Failed to stat source pushed image", "path", sourceWebpPath, "error", err)
				}
			}
		}
	}
	s.possiblyRender(ctx, &duplicatedApp, targetDevice, user)

	return nil
}
