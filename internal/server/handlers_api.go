package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/data"

	securejoin "github.com/cyphar/filepath-securejoin"
	"gorm.io/gorm"
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
	FirmwareVersion    string  `json:"firmwareVersion"`
	FirmwareType       string  `json:"firmwareType"`
	ProtocolVersion    *int    `json:"protocolVersion,omitempty"`
	MACAddress         string  `json:"macAddress"`
	ProtocolType       string  `json:"protocolType"`
	SSID               *string `json:"ssid,omitempty"`
	WifiPowerSave      *int    `json:"wifiPowerSave,omitempty"`
	SkipDisplayVersion *bool   `json:"skipDisplayVersion,omitempty"`
	APMode             *bool   `json:"apMode,omitempty"`
	PreferIPv6         *bool   `json:"preferIPv6,omitempty"`
	SwapColors         *bool   `json:"swapColors,omitempty"`
	ImageURL           *string `json:"imageUrl,omitempty"`
	Hostname           *string `json:"hostname,omitempty"`
	SNTPServer         *string `json:"sntpServer,omitempty"`
	SyslogAddr         *string `json:"syslogAddr,omitempty"`
}

// toDevicePayload converts a data.Device model to a DevicePayload for API responses.
func (s *Server) toDevicePayload(d *data.Device) DevicePayload {
	info := DeviceInfo{
		FirmwareVersion:    d.Info.FirmwareVersion,
		FirmwareType:       d.Info.FirmwareType,
		ProtocolVersion:    d.Info.ProtocolVersion,
		MACAddress:         d.Info.MACAddress,
		ProtocolType:       string(d.Info.ProtocolType),
		SSID:               d.Info.SSID,
		WifiPowerSave:      d.Info.WifiPowerSave,
		SkipDisplayVersion: d.Info.SkipDisplayVersion,
		APMode:             d.Info.APMode,
		PreferIPv6:         d.Info.PreferIPv6,
		SwapColors:         d.Info.SwapColors,
		ImageURL:           d.Info.ImageURL,
		Hostname:           d.Info.Hostname,
		SNTPServer:         d.Info.SNTPServer,
		SyslogAddr:         d.Info.SyslogAddr,
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
	user := GetUser(r)

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
	user := GetUser(r)
	device := GetDevice(r)

	var dataReq PushAppData
	if err := json.NewDecoder(r.Body).Decode(&dataReq); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Find App Path
	var appPath string

	// 1. Check System Apps
	for _, app := range s.ListSystemApps() {
		if app.ID == dataReq.AppID {
			appPath = filepath.Join(s.DataDir, app.Path)
			break
		}
	}

	// 2. Check User Apps
	if appPath == "" && user != nil {
		userApps := apps.ListUserApps(s.DataDir, user.Username)
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

	// Look up existing app if installationID is provided to get DisplayTime and filters
	var existingApp *data.App
	installationID := dataReq.InstallationID
	if installationID == "" {
		installationID = dataReq.InstallationIDAlt
	}
	if installationID != "" {
		existingApp = device.GetApp(installationID)
	}

	imgBytes, _, err := s.RenderApp(r.Context(), device, existingApp, appPath, dataReq.Config)
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
		if err := s.ensurePushedApp(r.Context(), device.ID, installationID); err != nil {
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
	device := GetDevice(r)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(s.toDevicePayload(device)); err != nil {
		slog.Error("Failed to encode device JSON", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

func (s *Server) handleListInstallations(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

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
	iname := r.PathValue("iname")

	device := GetDevice(r)

	app := device.GetApp(iname)
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
	device := GetDevice(r)

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
		if err := s.ensurePushedApp(r.Context(), device.ID, installID); err != nil {
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
	dir, err := s.ensureDeviceImageDir(deviceID)
	if err != nil {
		return fmt.Errorf("failed to get device webp directory: %w", err)
	}

	dir = filepath.Join(dir, "pushed")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	var filename string
	if installID != "" {
		filename = installID + ".webp"
	} else {
		filename = fmt.Sprintf("__%d.webp", time.Now().UnixNano())
	}

	path, err := securejoin.SecureJoin(dir, filename)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

func (s *Server) ensurePushedApp(ctx context.Context, deviceID, installID string) error {
	// Check if install exists
	count, err := gorm.G[data.App](s.DB).Where("device_id = ? AND iname = ?", deviceID, installID).Count(ctx, "*")
	if err != nil {
		slog.Error("Failed to check if app exists for image push", "error", err)
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

	maxOrder, err := getMaxAppOrder(s.DB, deviceID)
	if err != nil {
		slog.Error("Failed to get max app order", "error", err)
		// Non-fatal, default to 0 for order (if maxOrder is 0)
	}
	newApp.Order = maxOrder + 1

	return gorm.G[data.App](s.DB).Create(ctx, &newApp)
}

func (s *Server) handlePatchDevice(w http.ResponseWriter, r *http.Request) {
	// Auth handled by middleware, get device
	device := GetDevice(r)

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
			if device.GetApp(*update.NightModeApp) == nil {
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
			if device.GetApp(*update.PinnedApp) == nil {
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

	if err := s.DB.Omit("Apps").Save(device).Error; err != nil {
		http.Error(w, "Failed to update device", http.StatusInternalServerError)
		return
	}

	// Notify Dashboard
	user := GetUser(r)
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

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
	iname := r.PathValue("iname")

	device := GetDevice(r)

	app := device.GetApp(iname)
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
			webpDir, err := s.ensureDeviceImageDir(device.ID)
			if err != nil {
				slog.Error("Failed to get device webp directory for app disable cleanup", "device_id", device.ID, "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}
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
		if err := s.DB.Omit("Apps").Save(device).Error; err != nil {
			http.Error(w, "Failed to update device pin status", http.StatusInternalServerError)
			return
		}
	}

	if err := s.DB.Save(app).Error; err != nil {
		http.Error(w, "Failed to update app", http.StatusInternalServerError)
		return
	}

	// Notify Dashboard
	user := GetUser(r)
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(app); err != nil {
		slog.Error("Failed to encode app", "error", err)
	}
}

func (s *Server) handleDeleteInstallationAPI(w http.ResponseWriter, r *http.Request) {
	iname := filepath.Base(r.PathValue("iname"))

	device := GetDevice(r)
	if _, err := gorm.G[data.App](s.DB).Where("device_id = ? AND iname = ?", device.ID, iname).Delete(r.Context()); err != nil {
		http.Error(w, "Failed to delete app", http.StatusInternalServerError)
		return
	}

	// Clean up files
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	if err != nil {
		slog.Error("Failed to get device webp directory for app delete cleanup", "device_id", device.ID, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	matches, _ := filepath.Glob(filepath.Join(webpDir, fmt.Sprintf("*-%s.webp", iname)))
	for _, match := range matches {
		if err := os.Remove(match); err != nil {
			slog.Error("Failed to remove webp file", "path", match, "error", err)
		}
	}

	// Notify Dashboard
	user := GetUser(r)
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("App deleted.")); err != nil {
		slog.Error("Failed to write response", "error", err)
	}
}

func (s *Server) handleRebootDeviceAPI(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	if err := s.sendRebootCommand(device.ID); err != nil {
		slog.Error("Failed to send reboot command", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("Reboot command sent.")); err != nil {
		slog.Error("Failed to write response", "error", err)
	}
}

// FirmwareSettingsUpdate represents the updatable firmware settings via API.
type FirmwareSettingsUpdate struct {
	SkipDisplayVersion *bool   `json:"skipDisplayVersion"`
	PreferIPv6         *bool   `json:"preferIPv6"`
	APMode             *bool   `json:"apMode"`
	SwapColors         *bool   `json:"swapColors"`
	WifiPowerSave      *int    `json:"wifiPowerSave"`
	ImageURL           *string `json:"imageUrl"`
	Hostname           *string `json:"hostname"`
	SNTPServer         *string `json:"sntpServer"`
	SyslogAddr         *string `json:"syslogAddr"`
}

func (s *Server) handleUpdateFirmwareSettingsAPI(w http.ResponseWriter, r *http.Request) {
	device := GetDevice(r)

	var update FirmwareSettingsUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	payload := make(map[string]any)

	if update.SkipDisplayVersion != nil {
		payload["skip_display_version"] = *update.SkipDisplayVersion
	}
	if update.PreferIPv6 != nil {
		payload["prefer_ipv6"] = *update.PreferIPv6
	}
	if update.APMode != nil {
		payload["ap_mode"] = *update.APMode
	}
	if update.SwapColors != nil {
		payload["swap_colors"] = *update.SwapColors
	}
	if update.WifiPowerSave != nil {
		payload["wifi_power_save"] = *update.WifiPowerSave
	}
	if update.ImageURL != nil {
		payload["image_url"] = *update.ImageURL
	}
	if update.Hostname != nil {
		payload["hostname"] = *update.Hostname
	}
	if update.SNTPServer != nil {
		payload["sntp_server"] = *update.SNTPServer
	}
	if update.SyslogAddr != nil {
		payload["syslog_addr"] = *update.SyslogAddr
	}

	if len(payload) == 0 {
		http.Error(w, "No settings provided", http.StatusBadRequest)
		return
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal firmware settings payload", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	s.Broadcaster.Notify(device.ID, DeviceCommandMessage{Payload: jsonPayload})

	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("Firmware settings updated.")); err != nil {
		slog.Error("Failed to write response", "error", err)
	}
}

func (s *Server) SetupAPIRoutes() {
	// API v0 Group - authenticated with Middleware
	s.Router.Handle("GET /v0/devices", s.APIAuthMiddleware(http.HandlerFunc(s.handleListDevices)))
	s.Router.Handle("GET /v0/devices/{id}", s.APIAuthMiddleware(s.RequireDevice(s.handleGetDevice)))
	s.Router.Handle("POST /v0/devices/{id}/push", s.APIAuthMiddleware(s.RequireDevice(s.handlePushImage)))
	s.Router.Handle("POST /v0/devices/{id}/push_app", s.APIAuthMiddleware(s.RequireDevice(s.handlePushApp)))
	s.Router.Handle("POST /v0/devices/{id}/update_firmware_settings", s.APIAuthMiddleware(s.RequireDevice(s.handleUpdateFirmwareSettingsAPI)))
	s.Router.Handle("POST /v0/devices/{id}/reboot", s.APIAuthMiddleware(s.RequireDevice(s.handleRebootDeviceAPI)))
	s.Router.Handle("GET /v0/devices/{id}/installations", s.APIAuthMiddleware(s.RequireDevice(s.handleListInstallations)))
	s.Router.Handle("GET /v0/devices/{id}/installations/{iname}", s.APIAuthMiddleware(s.RequireDevice(s.handleGetInstallation)))
	s.Router.Handle("PATCH /v0/devices/{id}", s.APIAuthMiddleware(s.RequireDevice(s.handlePatchDevice)))
	s.Router.Handle("PATCH /v0/devices/{id}/installations/{iname}", s.APIAuthMiddleware(s.RequireDevice(s.handlePatchInstallation)))
	s.Router.Handle("DELETE /v0/devices/{id}/installations/{iname}", s.APIAuthMiddleware(s.RequireDevice(s.handleDeleteInstallationAPI)))
}
