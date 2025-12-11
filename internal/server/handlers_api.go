package server

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/renderer"
)

// --- API Handlers ---

// DeviceUpdate represents the updatable fields for a device via API.
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

// DevicePayload represents the full device data returned via API.
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

// NightMode represents night mode settings in the API payload.
type NightMode struct {
	Enabled    bool   `json:"enabled"`
	App        string `json:"app"`
	StartTime  string `json:"startTime"`
	EndTime    string `json:"endTime"`
	Brightness int    `json:"brightness"`
}

// DimMode represents dim mode settings in the API payload.
type DimMode struct {
	StartTime  *string `json:"startTime"`
	Brightness *int    `json:"brightness"`
}

// Interstitial represents interstitial app settings in the API payload.
type Interstitial struct {
	Enabled bool    `json:"enabled"`
	App     *string `json:"app"`
}

// DeviceInfo represents device firmware and protocol information in the API payload.
type DeviceInfo struct {
	FirmwareVersion string `json:"firmwareVersion"`
	FirmwareType    string `json:"firmwareType"`
	ProtocolVersion *int   `json:"protocolVersion"`
	MACAddress      string `json:"macAddress"`
	ProtocolType    string `json:"protocolType"`
}

// toDevicePayload converts a data.Device model to a DevicePayload for API responses.
func (s *Server) toDevicePayload(d *data.Device) DevicePayload {
	info := DeviceInfo{
		FirmwareVersion: d.Info.FirmwareVersion,
		FirmwareType:    d.Info.FirmwareType,
		ProtocolVersion: d.Info.ProtocolVersion,
		MACAddress:      d.Info.MACAddress,
		ProtocolType:    string(d.Info.ProtocolType),
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

// AppPayload represents the API response for an app installation.
type AppPayload struct {
	ID                string `json:"id"`
	AppID             string `json:"appID"`
	Enabled           bool   `json:"enabled"`
	Pinned            bool   `json:"pinned"`
	Pushed            bool   `json:"pushed"`
	RenderIntervalMin int    `json:"renderIntervalMin"`
	DisplayTimeSec    int    `json:"displayTimeSec"`
	LastRenderAt      int64  `json:"lastRenderAt"`
	IsInactive        bool   `json:"isInactive"`
}

func (s *Server) toAppPayload(device *data.Device, app *data.App) AppPayload {
	pinned := device.PinnedApp != nil && *device.PinnedApp == app.Iname
	return AppPayload{
		ID:                app.Iname,
		AppID:             app.Name,
		Enabled:           app.Enabled,
		Pinned:            pinned,
		Pushed:            app.Pushed,
		RenderIntervalMin: app.UInterval,
		DisplayTimeSec:    app.DisplayTime,
		LastRenderAt:      app.LastRender.Unix(),
		IsInactive:        app.EmptyLastRender,
	}
}

// ListDevicesPayload represents the response for listing devices.
type ListDevicesPayload struct {
	Devices []DevicePayload `json:"devices"`
}

// PushAppData represents the data for pushing an app configuration.
type PushAppData struct {
	Config            map[string]any `json:"config"`
	AppID             string         `json:"app_id"`
	InstallationID    string         `json:"installationID"`
	InstallationIDAlt string         `json:"installationId"`
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	user, err := UserFromContext(r.Context())
	if err != nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// If using an API key associated with a specific device, this endpoint might not make sense
	// or should return only that device. The legacy behavior (Python) returns all devices for the user.
	// Since APIAuthMiddleware populates user with all devices preloaded, we can just use that.

	devicePayloads := make([]DevicePayload, 0, len(user.Devices))
	for i := range user.Devices {
		devicePayloads = append(devicePayloads, s.toDevicePayload(&user.Devices[i]))
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(ListDevicesPayload{Devices: devicePayloads}); err != nil {
		slog.Error("Failed to encode devices JSON", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) handlePushApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	user, userErr := UserFromContext(r.Context())
	var device *data.Device
	if d, err := DeviceFromContext(r.Context()); err == nil {
		if d.ID != id {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		device = d
	} else if userErr == nil && user != nil {
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

	var dataReq PushAppData
	if err := json.NewDecoder(r.Body).Decode(&dataReq); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Find App Path
	var appPath string

	// 1. Check System Apps
	s.SystemAppsCacheMutex.RLock()
	for _, app := range s.SystemAppsCache {
		if app.ID == dataReq.AppID {
			appPath = filepath.Join(s.DataDir, app.Path)
			break
		}
	}
	s.SystemAppsCacheMutex.RUnlock()

	// 2. Check User Apps
	if appPath == "" && user != nil {
		userApps, _ := apps.ListUserApps(s.DataDir, user.Username)
		for _, app := range userApps {
			if app.ID == dataReq.AppID { // AppID for user apps is folder name
				appPath = filepath.Join(s.DataDir, app.Path)
				break
			}
		}
	}

	if appPath == "" {
		http.Error(w, "App not found", http.StatusNotFound)
		return
	}

	// Convert config to map[string]string
	configStr := make(map[string]string)
	for k, v := range dataReq.Config {
		configStr[k] = fmt.Sprintf("%v", v)
	}

	// Look up existing app if installationID is provided to get DisplayTime and filters
	var existingApp *data.App
	installationID := dataReq.InstallationID
	if installationID == "" {
		installationID = dataReq.InstallationIDAlt
	}
	if installationID != "" {
		for i := range device.Apps {
			if device.Apps[i].Iname == installationID {
				existingApp = &device.Apps[i]
				break
			}
		}
	}

	// Determine Dwell Time
	appInterval := device.DefaultInterval
	if existingApp != nil && existingApp.DisplayTime > 0 {
		appInterval = existingApp.DisplayTime
	}

	// Filters
	filters := s.getEffectiveFilters(device, existingApp)

	// Render
	deviceTimezone := device.GetTimezone()
	imgBytes, _, err := renderer.Render(
		r.Context(),
		appPath,
		configStr,
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
		slog.Error("Failed to render app", "error", err)
		http.Error(w, "Rendering failed", http.StatusInternalServerError)
		return
	}

	if len(imgBytes) == 0 {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("Empty image, not pushing")); err != nil {
			slog.Error("Failed to write empty image response", "error", err)
		}
		return
	}

	if installationID != "" {
		// Ensure app record exists
		if err := s.ensurePushedApp(device.ID, installationID); err != nil {
			slog.Error("Failed to ensure pushed app", "error", err)
		}
	}

	// Notify device via Websocket
	sent := s.Broadcaster.Notify(device.ID, imgBytes)

	if !sent || installationID != "" {
		if err := s.savePushedImage(device.ID, installationID, imgBytes); err != nil {
			http.Error(w, "Failed to save image", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("App pushed.")); err != nil {
		slog.Error("Failed to write response", "error", err)
	}
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

	installations := make([]AppPayload, 0, len(device.Apps))
	for i := range device.Apps {
		installations = append(installations, s.toAppPayload(device, &device.Apps[i]))
	}

	response := map[string]any{
		"installations": installations,
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		slog.Error("Failed to encode installations JSON", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) handleGetInstallation(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	iname := r.PathValue("iname")

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

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.toAppPayload(device, app)); err != nil {
		slog.Error("Failed to encode app JSON", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// PushData represents the data for pushing an image to a device.
type PushData struct {
	InstallationID    string `json:"installationID"`
	InstallationIDAlt string `json:"installationId"`
	Image             string `json:"image"`
}

func (s *Server) handlePushImage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	user, userErr := UserFromContext(r.Context())
	var device *data.Device
	if d, err := DeviceFromContext(r.Context()); err == nil {
		if d.ID != id {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
		device = d
	} else if userErr == nil && user != nil {
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

	if installID != "" {
		if err := s.ensurePushedApp(device.ID, installID); err != nil {
			slog.Error("Error adding pushed app", "error", err)
		}
	}

	// Notify device via Websocket
	sent := s.Broadcaster.Notify(device.ID, imgBytes)

	if !sent || installID != "" {
		if err := s.savePushedImage(device.ID, installID, imgBytes); err != nil {
			http.Error(w, fmt.Sprintf("Failed to save image: %v", err), http.StatusInternalServerError)
			return
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
		if *update.NightModeApp != "" {
			appExists := false
			for _, app := range device.Apps {
				if app.Iname == *update.NightModeApp {
					appExists = true
					break
				}
			}
			if !appExists {
				http.Error(w, "Night mode app not found", http.StatusBadRequest)
				return
			}
		}
		device.NightModeApp = *update.NightModeApp
	}
	if update.NightModeBrightness != nil {
		device.NightBrightness = data.Brightness(*update.NightModeBrightness)
	}
	if update.PinnedApp != nil {
		if *update.PinnedApp != "" {
			appExists := false
			for _, app := range device.Apps {
				if app.Iname == *update.PinnedApp {
					appExists = true
					break
				}
			}
			if !appExists {
				http.Error(w, "Pinned app not found", http.StatusBadRequest)
				return
			}
		}
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

	// Notify Dashboard
	if user, err := UserFromContext(r.Context()); err == nil {
		s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.toDevicePayload(device)); err != nil {
		slog.Error("Failed to encode device", "error", err)
	}
}

// InstallationUpdate represents the updatable fields for an app installation via API.
type InstallationUpdate struct {
	Enabled           *bool `json:"enabled"`
	Pinned            *bool `json:"pinned"`
	RenderIntervalMin *int  `json:"renderIntervalMin"`
	DisplayTimeSec    *int  `json:"displayTimeSec"`
}

func (s *Server) handlePatchInstallation(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	iname := r.PathValue("iname")

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
		if !app.Enabled {
			// Delete associated webp files when app is disabled
			webpDir := s.getDeviceWebPDir(device.ID)
			matches, _ := filepath.Glob(filepath.Join(webpDir, fmt.Sprintf("*-%s.webp", app.Iname)))
			for _, match := range matches {
				if err := os.Remove(match); err != nil {
					slog.Error("Failed to remove webp file on app disable", "path", match, "error", err)
				}
			}
			// Also check for pushed webp files
			pushedWebpPath := filepath.Join(webpDir, "pushed", fmt.Sprintf("%s.webp", app.Iname))
			if _, err := os.Stat(pushedWebpPath); err == nil {
				if err := os.Remove(pushedWebpPath); err != nil {
					slog.Error("Failed to remove pushed webp file on app disable", "path", pushedWebpPath, "error", err)
				}
			}
		} else {
			// Reset LastRender when app is enabled
			app.LastRender = time.Time{}
		}
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

	// Notify Dashboard
	if user, err := UserFromContext(r.Context()); err == nil {
		s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(app); err != nil {
		slog.Error("Failed to encode app", "error", err)
	}
}

func (s *Server) handleDeleteInstallationAPI(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")
	iname := filepath.Base(r.PathValue("iname"))

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
	// Security check for path traversal
	expectedPrefix := filepath.Join(s.DataDir, "installations") + string(os.PathSeparator)
	if strings.HasPrefix(filepath.Clean(installDir), expectedPrefix) {
		if err := os.RemoveAll(installDir); err != nil {
			slog.Error("Failed to remove install directory", "path", installDir, "error", err)
		}
	} else {
		slog.Warn("Potential path traversal detected in handleDeleteInstallationAPI", "iname", iname)
	}

	webpDir := s.getDeviceWebPDir(device.ID)
	matches, _ := filepath.Glob(filepath.Join(webpDir, fmt.Sprintf("*-%s.webp", iname)))
	for _, match := range matches {
		if err := os.Remove(match); err != nil {
			slog.Error("Failed to remove webp file", "path", match, "error", err)
		}
	}

	// Notify Dashboard
	if user, err := UserFromContext(r.Context()); err == nil {
		s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("App deleted.")); err != nil {
		slog.Error("Failed to write response", "error", err)
	}
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


func (s *Server) SetupAPIRoutes() {
	// API v0 Group - authenticated with Middleware
	s.Router.Handle("GET /v0/devices", s.APIAuthMiddleware(http.HandlerFunc(s.handleListDevices)))
	s.Router.Handle("GET /v0/devices/{id}", s.APIAuthMiddleware(http.HandlerFunc(s.handleGetDevice)))
	s.Router.Handle("POST /v0/devices/{id}/push", s.APIAuthMiddleware(http.HandlerFunc(s.handlePushImage)))
	s.Router.Handle("POST /v0/devices/{id}/push_app", s.APIAuthMiddleware(http.HandlerFunc(s.handlePushApp)))
	s.Router.Handle("GET /v0/devices/{id}/installations", s.APIAuthMiddleware(http.HandlerFunc(s.handleListInstallations)))
	s.Router.Handle("GET /v0/devices/{id}/installations/{iname}", s.APIAuthMiddleware(http.HandlerFunc(s.handleGetInstallation)))
	s.Router.Handle("PATCH /v0/devices/{id}", s.APIAuthMiddleware(http.HandlerFunc(s.handlePatchDevice)))
	s.Router.Handle("PATCH /v0/devices/{id}/installations/{iname}", s.APIAuthMiddleware(http.HandlerFunc(s.handlePatchInstallation)))
	s.Router.Handle("DELETE /v0/devices/{id}/installations/{iname}", s.APIAuthMiddleware(http.HandlerFunc(s.handleDeleteInstallationAPI)))

	s.Router.HandleFunc("GET /dots", s.handleDots)
}
