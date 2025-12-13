package server

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/gitutils"
	"tronbyt-server/internal/renderer"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

// AppManifest reflects a subset of manifest.yaml for internal updates.
type AppManifest struct {
	Broken       *bool   `yaml:"broken,omitempty"`
	BrokenReason *string `yaml:"brokenReason,omitempty"`
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

	// 1. Find App Details (Recommended Interval)
	recommendedInterval := 15 // Default

	// Check System Apps Cache
	s.SystemAppsCacheMutex.RLock()
	for _, app := range s.SystemAppsCache {
		if app.ID == appName {
			recommendedInterval = app.RecommendedInterval
			break
		}
	}
	s.SystemAppsCacheMutex.RUnlock()

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
	realPath := filepath.Join(s.DataDir, appPath)
	// Safety check: ensure it's inside data dir
	absDataDir, _ := filepath.Abs(s.DataDir)
	absRealPath, _ := filepath.Abs(realPath)
	if len(absRealPath) < len(absDataDir) || absRealPath[:len(absDataDir)] != absDataDir {
		slog.Warn("Potential path traversal", "path", realPath)
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

	if err := s.DB.Create(&newApp).Error; err != nil {
		slog.Error("Failed to save app", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// If WebP, copy file and redirect to index
	isWebP := strings.EqualFold(filepath.Ext(appPath), ".webp")
	if isWebP {
		destDir := s.getDeviceWebPDir(device.ID)
		destPath := filepath.Join(destDir, fmt.Sprintf("%s-%s.webp", newApp.Name, newApp.Iname))

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

func (s *Server) handleSystemAppThumbnail(w http.ResponseWriter, r *http.Request) {
	file := r.URL.Query().Get("file")
	if file == "" {
		http.Error(w, "File required", http.StatusBadRequest)
		return
	}

	baseDir := filepath.Join(s.DataDir, "system-apps", "apps")
	path := filepath.Clean(filepath.Join(baseDir, file))

	if !strings.HasPrefix(path, baseDir+string(os.PathSeparator)) {
		http.Error(w, "Invalid file path", http.StatusBadRequest)
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
		appPath := s.resolveAppPath(*app.Path)
		var err error
		schemaBytes, err = renderer.GetSchema(appPath, 64, 32, device.Type.Supports2x())
		if err != nil {
			slog.Error("Failed to get app schema", "error", err)
			// Fall through with empty schema
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
		app.StartTime = &payload.StartTime
	} else {
		app.StartTime = nil
	}
	if payload.EndTime != "" {
		app.EndTime = &payload.EndTime
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
		appInterval := device.GetEffectiveDwellTime(app)

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

	appInterval := device.GetEffectiveDwellTime(app)

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

	// Delete App
	if err := s.DB.Delete(app).Error; err != nil {
		slog.Error("Failed to delete app", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Clean up webp
	webpDir := s.getDeviceWebPDir(device.ID)
	matches, _ := filepath.Glob(filepath.Join(webpDir, fmt.Sprintf("*- %s.webp", app.Iname)))
	for _, match := range matches {
		if err := os.Remove(match); err != nil {
			slog.Error("Failed to remove app webp file", "path", match, "error", err)
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
		if err := s.DB.Model(device).Update("pinned_app", nil).Error; err != nil {
			slog.Error("Failed to unpin app", "error", err)
		}
	} else {
		if err := s.DB.Model(device).Update("pinned_app", newPinned).Error; err != nil {
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
	if err := s.DB.Model(app).Update("enabled", app.Enabled).Error; err != nil {
		slog.Error("Failed to toggle enabled", "error", err)
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
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDuplicateApp(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)
	originalApp := GetApp(r)

	var newIname string
	var err error

	newIname, err = generateUniqueIname(s.DB, device.ID)
	if err != nil {
		slog.Error("Failed to generate unique iname", "error", err)
		http.Error(w, "Could not generate unique iname", http.StatusInternalServerError)
		return
	}

	// Copy App
	newApp := *originalApp
	newApp.ID = 0 // GORM will generate new ID
	newApp.Iname = newIname
	newApp.LastRender = time.Time{}
	newApp.Order = originalApp.Order + 1
	newApp.Pushed = false

	// Transaction for reordering and creating
	err = s.DB.Transaction(func(tx *gorm.DB) error {
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
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleDuplicateAppToDevice(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

	sourceDeviceID := r.PathValue("source_device_id")
	targetDeviceID := r.PathValue("target_device_id")
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

func (s *Server) duplicateAppToDeviceLogic(r *http.Request, user *data.User, sourceDevice *data.Device, originalApp *data.App, targetDevice *data.Device, targetIname string, insertAfter bool) error {
	newIname, err := generateUniqueIname(s.DB, targetDevice.ID)
	if err != nil {
		return fmt.Errorf("failed to generate unique iname: %w", err)
	}

	duplicatedApp := *originalApp
	duplicatedApp.ID = 0
	duplicatedApp.DeviceID = targetDevice.ID
	duplicatedApp.Iname = newIname
	duplicatedApp.LastRender = time.Time{}
	duplicatedApp.Order = 0
	duplicatedApp.AutoPin = false

	if originalApp.Config != nil {
		newConfig := make(data.JSONMap)
		maps.Copy(newConfig, originalApp.Config)
		duplicatedApp.Config = newConfig
	}

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

	appsList := make([]data.App, len(targetDevice.Apps))
	copy(appsList, targetDevice.Apps)
	sort.Slice(appsList, func(i, j int) bool {
		return appsList[i].Order < appsList[j].Order
	})

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
		appsList = append(appsList[:insertIdx], append([]data.App{duplicatedApp}, appsList[insertIdx:]...)...)
	} else {
		appsList = append(appsList, duplicatedApp)
	}

	for i := range appsList {
		appsList[i].Order = i
	}
	targetDevice.Apps = appsList

	if originalApp.Pushed {
		sourceWebpDir := s.getDeviceWebPDir(sourceDevice.ID)
		sourceWebpPath := filepath.Join(sourceWebpDir, "pushed", fmt.Sprintf("%s.webp", originalApp.Iname))

		targetWebpDir := s.getDeviceWebPDir(targetDevice.ID)
		targetPushedWebpDir := filepath.Join(targetWebpDir, "pushed")
		if err := os.MkdirAll(targetPushedWebpDir, 0755); err != nil {
			slog.Error("Failed to create target pushed webp directory", "path", targetPushedWebpDir, "error", err)
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

	if err := s.DB.Save(user).Error; err != nil {
		return fmt.Errorf("failed to save user after duplicating app: %w", err)
	}

	s.possiblyRender(r.Context(), &duplicatedApp, targetDevice, user)

	// Notify Dashboard about target device update
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: targetDevice.ID})

	return nil
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
	if ext != ".star" && ext != ".webp" {
		http.Error(w, "Invalid file type", http.StatusBadRequest)
		return
	}

	appName := strings.TrimSuffix(filename, ext)

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
	_ = dst.Sync()

	previewDir := filepath.Join(s.DataDir, "apps")
	if err := os.MkdirAll(previewDir, 0755); err != nil {
		slog.Error("Failed to create preview dir", "error", err)
	}

	switch ext {
	case ".star":
		imgBytes, _, err := renderer.Render(
			r.Context(),
			dstPath,
			nil,
			64, 32,
			15*time.Second,
			30*time.Second,
			true,
			false,
			nil,
			nil,
			nil,
		)

		if err == nil && len(imgBytes) > 0 {
			previewPath := filepath.Join(previewDir, fmt.Sprintf("%s.webp", appName))
			if err := os.WriteFile(previewPath, imgBytes, 0644); err != nil {
				slog.Error("Failed to write preview image", "error", err)
			}
		} else {
			slog.Error("Failed to render preview", "error", err)
		}

	case ".webp":
		previewPath := filepath.Join(previewDir, fmt.Sprintf("%s.webp", appName))

		src, err := os.Open(dstPath)
		if err == nil {
			defer func() {
				if err := src.Close(); err != nil {
					slog.Error("Failed to close preview source", "error", err)
				}
			}()
			dstPreview, err := os.Create(previewPath)
			if err == nil {
				defer func() {
					if err := dstPreview.Close(); err != nil {
						slog.Error("Failed to close preview file", "error", err)
					}
				}()
				if _, err := io.Copy(dstPreview, src); err != nil {
					slog.Error("Failed to copy preview webp", "error", err)
				}
			} else {
				slog.Error("Failed to create preview file", "error", err)
			}
		} else {
			slog.Error("Failed to open uploaded webp for preview", "error", err)
		}
	}

	http.Redirect(w, r, fmt.Sprintf("/devices/%s/addapp", device.ID), http.StatusSeeOther)
}

func (s *Server) handleDeleteUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	filename := filepath.Base(r.PathValue("filename"))

	user := GetUser(r)

	// user.Devices is already preloaded by RequireLogin -> RequireDevice -> RequireUser, but GetUser in current implementation doesn't preload.
	// We need to fetch user with devices and apps here.
	var userWithDevices data.User
	if err := s.DB.Preload("Devices.Apps").First(&userWithDevices, "username = ?", user.Username).Error; err != nil {
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
	appDir := filepath.Join(userAppsPath, appName)

	if !strings.HasPrefix(appDir, userAppsPath+string(os.PathSeparator)) {
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

	manifestPath := filepath.Join(s.DataDir, "system-apps", "apps", appID, "manifest.yaml")

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
