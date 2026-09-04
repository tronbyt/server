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
	"strconv"
	"strings"
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
	NightModeActive     *bool   `json:"nightModeActive"`
	NightModeApp        *string `json:"nightModeApp"`
	NightModeBrightness *int    `json:"nightModeBrightness"`
	NightModeStartTime  *string `json:"nightModeStartTime"`
	NightModeEndTime    *string `json:"nightModeEndTime"`
	DimModeActive       *bool   `json:"dimModeActive"`
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
	Enabled       bool    `json:"enabled"`
	Active        bool    `json:"active"`
	App           string  `json:"app"`
	StartTime     string  `json:"startTime"`
	EndTime       string  `json:"endTime"`
	Brightness    int     `json:"brightness"`
	OverrideUntil *string `json:"overrideUntil,omitempty"`
}

// DimMode represents dim mode settings in the API payload.
type DimMode struct {
	Enabled       bool    `json:"enabled"`
	Active        bool    `json:"active"`
	StartTime     *string `json:"startTime"`
	Brightness    *int    `json:"brightness"`
	OverrideUntil *string `json:"overrideUntil,omitempty"`
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
	SkipBootAnimation  *bool   `json:"skipBootAnimation,omitempty"`
	APMode             *bool   `json:"apMode,omitempty"`
	PreferIPv6         *bool   `json:"preferIPv6,omitempty"`
	SwapColors         *bool   `json:"swapColors,omitempty"`
	DisableTouch       *bool   `json:"disableTouch,omitempty"`
	ImageURL           *string `json:"imageUrl,omitempty"`
	Hostname           *string `json:"hostname,omitempty"`
	SNTPServer         *string `json:"sntpServer,omitempty"`
	SyslogAddr         *string `json:"syslogAddr,omitempty"`
}

// toDevicePayload converts a data.Device model to a DevicePayload for API responses.
func (s *Server) toDevicePayload(d *data.Device) DevicePayload {
	now := deviceTimeNow(d)
	info := DeviceInfo{
		FirmwareVersion:    d.Info.FirmwareVersion,
		FirmwareType:       d.Info.FirmwareType,
		ProtocolVersion:    d.Info.ProtocolVersion,
		MACAddress:         d.Info.MACAddress,
		ProtocolType:       string(d.Info.ProtocolType),
		SSID:               d.Info.SSID,
		WifiPowerSave:      d.Info.WifiPowerSave,
		SkipDisplayVersion: d.Info.SkipDisplayVersion,
		SkipBootAnimation:  d.Info.SkipBootAnimation,
		APMode:             d.Info.APMode,
		PreferIPv6:         d.Info.PreferIPv6,
		SwapColors:         d.Info.SwapColors,
		DisableTouch:       d.Info.DisableTouch,
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

	var nightModeOverrideUntil *string
	if d.GetNightModeOverrideActiveAt(now) && d.NightModeOverrideUntil != nil {
		formatted := d.NightModeOverrideUntil.In(now.Location()).Format(time.RFC3339)
		nightModeOverrideUntil = &formatted
	}
	var dimModeOverrideUntil *string
	if d.GetDimModeOverrideActiveAt(now) && d.DimModeOverrideUntil != nil {
		formatted := d.DimModeOverrideUntil.In(now.Location()).Format(time.RFC3339)
		dimModeOverrideUntil = &formatted
	}

	return DevicePayload{
		ID:          d.ID,
		Type:        d.Type,
		DisplayName: d.Name,
		Notes:       d.Notes,
		IntervalSec: d.DefaultInterval,
		Brightness:  int(d.Brightness),
		NightMode: NightMode{
			Enabled:       d.NightModeEnabled,
			Active:        d.GetNightModeIsActive(),
			App:           d.NightModeApp,
			StartTime:     d.NightStart,
			EndTime:       d.NightEnd,
			Brightness:    int(d.NightBrightness),
			OverrideUntil: nightModeOverrideUntil,
		},
		DimMode: DimMode{
			Enabled:       d.DimModeEnabled,
			Active:        d.GetDimModeIsActive(),
			StartTime:     d.DimTime,
			Brightness:    dimBrightnessPtr,
			OverrideUntil: dimModeOverrideUntil,
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

	// Schedule fields
	StartTime *string  `json:"startTime"`
	EndTime   *string  `json:"endTime"`
	Days      []string `json:"days"`

	// Recurrence fields
	UseCustomRecurrence bool                `json:"useCustomRecurrence"`
	RecurrenceType      data.RecurrenceType `json:"recurrenceType"`
	RecurrenceInterval  int                 `json:"recurrenceInterval"`
	RecurrencePattern   map[string]any      `json:"recurrencePattern"`
	RecurrenceStartDate *string             `json:"recurrenceStartDate"`
	RecurrenceEndDate   *string             `json:"recurrenceEndDate"`

	// Render behavior. Config is deliberately absent: it holds whatever the
	// app's schema defines, which for many apps includes API keys and OAuth
	// tokens, and a device API key is a lower bar than a session. It can be
	// written via PATCH but is never read back.
	AutoPin           bool              `json:"autoPin"`
	ColorFilter       *data.ColorFilter `json:"colorFilter"`
	ShowFullAnimation *bool             `json:"showFullAnimation"`
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

		StartTime: app.StartTime,
		EndTime:   app.EndTime,
		Days:      app.Days,

		UseCustomRecurrence: app.UseCustomRecurrence,
		RecurrenceType:      app.RecurrenceType,
		RecurrenceInterval:  app.RecurrenceInterval,
		RecurrencePattern:   app.RecurrencePattern,
		RecurrenceStartDate: app.RecurrenceStartDate,
		RecurrenceEndDate:   app.RecurrenceEndDate,

		AutoPin:           app.AutoPin,
		ColorFilter:       app.ColorFilter,
		ShowFullAnimation: app.ShowFullAnimation,
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
	CoalesceID        string         `json:"coalesceID"`
	Background        bool           `json:"background"`
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

	// Determine installationID
	installationID := dataReq.InstallationID
	if installationID == "" {
		installationID = dataReq.InstallationIDAlt
	}

	// Look up existing pushed app first (by path), then fall back to iname lookup.
	var existingApp *data.App
	var appPath string
	if installationID != "" {
		existingApp = device.GetPushedApp(installationID)
		if existingApp == nil {
			existingApp = device.GetApp(installationID)
		}
		// Only derive appPath from non-pushed installations; "pushed:<id>" is not a real app path.
		if existingApp != nil && existingApp.Path != nil && *existingApp.Path != "" &&
			!strings.HasPrefix(*existingApp.Path, "pushed:") {
			appPath, _ = securejoin.SecureJoin(s.DataDir, *existingApp.Path)
		}
	}

	// For pushed apps with a cached image and no new config/app being sent, skip
	// re-rendering and re-push the existing image directly.
	if existingApp != nil && existingApp.Pushed &&
		existingApp.Path != nil && strings.HasPrefix(*existingApp.Path, "pushed:") &&
		len(dataReq.Config) == 0 && dataReq.AppID == "" {
		cachedID := strings.TrimPrefix(*existingApp.Path, "pushed:")
		pushedImagePath, err := securejoin.SecureJoin(filepath.Join(s.DataDir, "webp", device.ID, "pushed"), cachedID+".webp")
		if err != nil {
			slog.Error("Failed to resolve pushed image path", "error", err)
			http.Error(w, "Image not found", http.StatusNotFound)
			return
		}
		imgBytes, err := os.ReadFile(pushedImagePath)
		if err != nil {
			// No cached image — fall through to the render path so the caller can
			// recover by providing an appID and config.
			slog.Warn("Cached pushed image missing, falling through to render", "path", pushedImagePath)
		} else {
			if !dataReq.Background {
				s.Broadcaster.Notify(device.ID, imgBytes)
			}
			if err := s.ensurePushedApp(r.Context(), device.ID, cachedID); err != nil {
				slog.Error("Error adding pushed app", "error", err)
			}
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("App pushed.")); err != nil {
				slog.Error("Failed to write response", "error", err)
			}
			return
		}
	}

	// If we couldn't get appPath from installation, look it up by app_id
	if appPath == "" {
		if dataReq.AppID == "" {
			http.Error(w, "app_id is required when no valid installationID is provided", http.StatusBadRequest)
			return
		}

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

	// Notify device via Websocket only if this is a foreground push
	sent := false
	if !dataReq.Background {
		sent = s.Broadcaster.Notify(device.ID, imgBytes)
	}

	if !sent || installationID != "" {
		if err := s.savePushedImage(device.ID, installationID, dataReq.CoalesceID, imgBytes); err != nil {
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
		installations = append(installations, s.toAppPayload(device, device.Apps[i]))
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
		// Fall back to resolving pushed apps by their user-supplied
		// installationID, matching handleDeleteInstallationAPI's behavior.
		app = device.GetPushedApp(iname)
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
	CoalesceID        string `json:"coalesceID"`
	Image             string `json:"image"`
	Background        bool   `json:"background"`
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

	// Notify device via Websocket only if this is a foreground push
	sent := false
	if !dataReq.Background {
		sent = s.Broadcaster.Notify(device.ID, imgBytes)
	}

	if !sent || installID != "" {
		if err := s.savePushedImage(device.ID, installID, dataReq.CoalesceID, imgBytes); err != nil {
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

func (s *Server) savePushedImage(deviceID, installID, coalesceID string, data []byte) error {
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
		// Image push with installID: stable filename, always replaces
		filename = installID + ".webp"
	} else if coalesceID != "" {
		// Validate coalesceID to prevent path traversal and suffix collisions.
		if len(coalesceID) > 64 {
			return fmt.Errorf("coalesceID exceeds maximum length of 64 characters")
		}
		for _, r := range coalesceID {
			isValid := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
			if !isValid {
				return fmt.Errorf("coalesceID contains invalid characters (only alphanumeric, underscore, and dash allowed)")
			}
		}

		// Coalesced push: delete existing file with same coalesceID, then save.
		// At most 1 pending push per coalesceID.
		// Filename format: __{timestamp}_{coalesceID}.webp
		// Extract coalesceID by splitting at the first underscore after the "__" prefix.
		if entries, err := os.ReadDir(dir); err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if !entry.IsDir() && strings.HasPrefix(name, "__") && strings.HasSuffix(name, ".webp") {
					inner := name[2 : len(name)-5] // strip "__" and ".webp"
					if _, fileCoalesceID, found := strings.Cut(inner, "_"); found {
						if fileCoalesceID == coalesceID {
							if err := os.Remove(filepath.Join(dir, name)); err != nil {
								slog.Warn("Failed to remove coalesced push", "name", name, "error", err)
							}
						}
					}
				}
			}
		}
		filename = fmt.Sprintf("__%d_%s.webp", time.Now().UnixNano(), coalesceID)
	} else {
		// Anonymous push: unbounded ephemeral queue
		filename = fmt.Sprintf("__%d.webp", time.Now().UnixNano())
	}

	path, err := securejoin.SecureJoin(dir, filename)
	if err != nil {
		return err
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return err
	}

	// Clean up anonymous ephemeral files older than 24 hours.
	// Only runs when saving an anonymous push (coalesced and installID-based
	// pushes are already bounded). Runs in a background goroutine to avoid
	// blocking the HTTP response.
	if installID == "" && coalesceID == "" {
		go func() {
			cutoff := time.Now().UnixNano() - 24*int64(time.Hour)
			if entries, readErr := os.ReadDir(dir); readErr == nil {
				for _, entry := range entries {
					name := entry.Name()
					if !entry.IsDir() && strings.HasPrefix(name, "__") && strings.HasSuffix(name, ".webp") {
						// Anonymous pushes: __{nanos}.webp (no underscore between __ and .webp suffix)
						inner := name[2 : len(name)-5]
						if strings.Contains(inner, "_") {
							continue // coalesced push, not anonymous
						}
						if ts, parseErr := strconv.ParseInt(inner, 10, 64); parseErr == nil && ts < cutoff {
							if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
								slog.Warn("Failed to remove expired ephemeral image", "name", name, "error", err)
							}
						}
					}
				}
			}
		}()
	}

	return nil
}

func (s *Server) ensurePushedApp(ctx context.Context, deviceID, installID string) error {
	// Check if app exists by matching on installID (for pushed apps, we need to look up by installID)
	// Since installID might be non-numeric (e.g., "pushed:hasssolarlocal1"), we check via path/file
	count, err := gorm.G[data.App](s.DB).Where("device_id = ? AND pushed = ? AND path = ?", deviceID, true, "pushed:"+installID).Count(ctx, "*")
	if err != nil {
		slog.Error("Failed to check if app exists for image push", "error", err)
		return err
	}
	if count > 0 {
		return nil
	}

	// Generate a numeric iname for the pushed app (same as regular apps)
	newIname, err := generateUniqueIname(s.DB, deviceID)
	if err != nil {
		slog.Error("Failed to generate iname for pushed app", "error", err)
		return err
	}

	// Store installID in path so we can match on it later
	installPath := "pushed:" + installID

	newApp := data.App{
		DeviceID:    deviceID,
		Iname:       newIname,
		Name:        "pushed",
		UInterval:   10,
		DisplayTime: 0,
		Enabled:     true,
		Pushed:      true,
		Path:        &installPath,
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
	nightModeWasEnabled := device.NightModeEnabled
	modeSnapshotBefore := snapshotDeviceMode(device)
	nightStartWas := device.NightStart
	nightEndWas := device.NightEnd
	dimModeWasEnabled := device.DimModeEnabled
	var dimTimeWas string
	if device.DimTime != nil {
		dimTimeWas = *device.DimTime
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

	if !device.NightModeEnabled || nightModeWasEnabled != device.NightModeEnabled || nightStartWas != device.NightStart || nightEndWas != device.NightEnd {
		clearNightModeOverride(device)
	}
	if update.NightModeActive != nil {
		if _, err := setNightModeOverride(device, *update.NightModeActive); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	currentDimTime := ""
	if device.DimTime != nil {
		currentDimTime = *device.DimTime
	}
	if !device.DimModeEnabled || dimModeWasEnabled != device.DimModeEnabled || dimTimeWas != currentDimTime {
		clearDimModeOverride(device)
	}
	if update.DimModeActive != nil {
		if _, err := setDimModeOverride(device, *update.DimModeActive); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}

	if err := s.DB.Omit("Apps").Save(device).Error; err != nil {
		http.Error(w, "Failed to update device", http.StatusInternalServerError)
		return
	}

	// Notify Dashboard
	user := GetUser(r)
	s.invalidateDeviceAppRendersIfModeChanged(r.Context(), device, modeSnapshotBefore)
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

	// Schedule fields
	StartTime *string   `json:"startTime"`
	EndTime   *string   `json:"endTime"`
	Days      *[]string `json:"days"`

	// Recurrence fields
	UseCustomRecurrence *bool                `json:"useCustomRecurrence"`
	RecurrenceType      *data.RecurrenceType `json:"recurrenceType"`
	RecurrenceInterval  *int                 `json:"recurrenceInterval"`
	RecurrencePattern   *map[string]any      `json:"recurrencePattern"`
	RecurrenceStartDate *string              `json:"recurrenceStartDate"`
	RecurrenceEndDate   *string              `json:"recurrenceEndDate"`

	// Render behavior
	AutoPin           *bool   `json:"autoPin"`
	ColorFilter       *string `json:"colorFilter"`       // "" or "inherit" clears
	ShowFullAnimation *string `json:"showFullAnimation"` // "auto" clears; else a bool

	// App config, matching what the config page already stores. Write-only:
	// GET never returns it, because it commonly holds API keys and OAuth
	// tokens. Replaces the whole map, as the config page does -- there is no
	// per-key merge.
	Config *map[string]any `json:"config"`
}

func (s *Server) handlePatchInstallation(w http.ResponseWriter, r *http.Request) {
	iname := r.PathValue("iname")

	device := GetDevice(r)

	app := device.GetApp(iname)
	if app == nil {
		// Fall back to resolving pushed apps by their user-supplied
		// installationID, matching handleDeleteInstallationAPI's behavior.
		app = device.GetPushedApp(iname)
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

	if update.RenderIntervalMin != nil {
		app.UInterval = *update.RenderIntervalMin
	}
	if update.DisplayTimeSec != nil {
		app.DisplayTime = *update.DisplayTimeSec
	}

	// Schedule fields
	if update.StartTime != nil {
		if *update.StartTime == "" {
			app.StartTime = nil
		} else {
			parsed, err := parseTimeInput(*update.StartTime)
			if err != nil {
				http.Error(w, fmt.Sprintf("Invalid startTime: %v", err), http.StatusBadRequest)
				return
			}
			app.StartTime = &parsed
		}
	}
	if update.EndTime != nil {
		if *update.EndTime == "" {
			app.EndTime = nil
		} else {
			parsed, err := parseTimeInput(*update.EndTime)
			if err != nil {
				http.Error(w, fmt.Sprintf("Invalid endTime: %v", err), http.StatusBadRequest)
				return
			}
			app.EndTime = &parsed
		}
	}
	if update.Days != nil {
		for _, day := range *update.Days {
			switch day {
			case "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday":
				// valid
			default:
				http.Error(w, fmt.Sprintf("Invalid day: %s", day), http.StatusBadRequest)
				return
			}
		}
		app.Days = *update.Days
	}

	// Recurrence fields
	if update.UseCustomRecurrence != nil {
		app.UseCustomRecurrence = *update.UseCustomRecurrence
	}
	if update.RecurrenceType != nil {
		switch *update.RecurrenceType {
		case data.RecurrenceDaily, data.RecurrenceWeekly, data.RecurrenceMonthly, data.RecurrenceYearly:
			app.RecurrenceType = *update.RecurrenceType
		default:
			http.Error(w, "Invalid recurrenceType", http.StatusBadRequest)
			return
		}
	}
	if update.RecurrenceInterval != nil {
		app.RecurrenceInterval = *update.RecurrenceInterval
	}
	if update.RecurrencePattern != nil {
		app.RecurrencePattern = *update.RecurrencePattern
	}
	if update.RecurrenceStartDate != nil {
		if *update.RecurrenceStartDate == "" {
			app.RecurrenceStartDate = nil
		} else {
			if _, err := time.Parse("2006-01-02", *update.RecurrenceStartDate); err != nil {
				http.Error(w, "Invalid recurrenceStartDate: must be YYYY-MM-DD", http.StatusBadRequest)
				return
			}
			app.RecurrenceStartDate = update.RecurrenceStartDate
		}
	}
	if update.RecurrenceEndDate != nil {
		if *update.RecurrenceEndDate == "" {
			app.RecurrenceEndDate = nil
		} else {
			if _, err := time.Parse("2006-01-02", *update.RecurrenceEndDate); err != nil {
				http.Error(w, "Invalid recurrenceEndDate: must be YYYY-MM-DD", http.StatusBadRequest)
				return
			}
			app.RecurrenceEndDate = update.RecurrenceEndDate
		}
	}

	// Render behavior
	if update.AutoPin != nil {
		app.AutoPin = *update.AutoPin
	}
	if update.ColorFilter != nil {
		switch *update.ColorFilter {
		case "", string(data.ColorFilterInherit):
			app.ColorFilter = nil
		default:
			if !s.isValidColorFilter(*update.ColorFilter) {
				http.Error(w, "Invalid colorFilter", http.StatusBadRequest)
				return
			}
			val := data.ColorFilter(*update.ColorFilter)
			app.ColorFilter = &val
		}
	}
	if update.ShowFullAnimation != nil {
		switch *update.ShowFullAnimation {
		case "", "auto":
			app.ShowFullAnimation = nil
		default:
			val, err := strconv.ParseBool(*update.ShowFullAnimation)
			if err != nil {
				http.Error(w, `Invalid showFullAnimation: want "auto", "true" or "false"`, http.StatusBadRequest)
				return
			}
			app.ShowFullAnimation = &val
		}
	}
	if update.Config != nil {
		app.Config = *update.Config
	}

	// Side effects last. Everything above either validates or stages an
	// in-memory change, so a request carrying one good field and one bad one
	// returns 400 without having deleted a render or repinned the device.
	if update.Enabled != nil {
		app.Enabled = *update.Enabled
		if app.Enabled {
			// Reset LastRender when app is enabled
			app.LastRender = time.Time{}
		}
	}
	if update.Pinned != nil {
		if *update.Pinned {
			device.PinnedApp = &app.Iname
		} else if device.PinnedApp != nil && *device.PinnedApp == app.Iname {
			device.PinnedApp = nil
		}
	}

	// The pin lives on the device row and everything else on the app row, so
	// write both in one transaction rather than letting a failed app save
	// leave the pin moved.
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if update.Pinned != nil {
			var pinned any
			if device.PinnedApp != nil {
				pinned = *device.PinnedApp
			}
			if _, err := gorm.G[data.Device](tx).Where("id = ?", device.ID).Update(r.Context(), "pinned_app", pinned); err != nil {
				return fmt.Errorf("update device pin status: %w", err)
			}
		}
		return tx.Save(app).Error
	}); err != nil {
		slog.Error("Failed to update installation", "device_id", device.ID, "iname", app.Iname, "error", err)
		http.Error(w, "Failed to update app", http.StatusInternalServerError)
		return
	}

	// Render cleanup is post-commit reconciliation: deleting an app's webp
	// files cannot be rolled back, so it waits until the disable is durable.
	// A failure here leaves stale files, not a wrong app state, so it logs
	// rather than failing a request that already succeeded.
	if update.Enabled != nil && !app.Enabled {
		s.removeAppRenders(device.ID, app.Iname)
	}

	// Notify Dashboard
	user := GetUser(r)
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	w.Header().Set("Content-Type", "application/json")
	// Respond with the same shape GET returns. This used to encode data.App
	// directly, which carried the app's whole config -- API keys and OAuth
	// tokens included -- back out over the wire, contradicting the omission
	// of config from the GET payload.
	if err := json.NewEncoder(w).Encode(s.toAppPayload(device, app)); err != nil {
		slog.Error("Failed to encode app", "error", err)
	}
}

// removeAppRenders deletes the rendered webp files belonging to an
// installation, both the timestamped renders and any pushed image.
func (s *Server) removeAppRenders(deviceID, iname string) {
	// iname becomes a path component here. Every app the UI and the API
	// create gets a server-generated numeric iname, but handleImportDeviceConfig
	// stores whatever an uploaded config carries, so treat it as untrusted:
	// only a plain filename component is safe to build a path from.
	if !isSafePathComponent(iname) {
		slog.Error("Refusing render cleanup for unsafe installation name", "device_id", deviceID, "iname", iname)
		return
	}

	webpDir, err := s.ensureDeviceImageDir(deviceID)
	if err != nil {
		slog.Error("Failed to get device webp directory for app disable cleanup", "device_id", deviceID, "error", err)
		return
	}

	// Match on the name rather than globbing: glob metacharacters in an
	// imported iname would otherwise widen the pattern past this app's files.
	entries, err := os.ReadDir(webpDir)
	if err != nil {
		slog.Error("Failed to read device webp directory for app disable cleanup", "device_id", deviceID, "error", err)
		return
	}
	suffix := fmt.Sprintf("-%s.webp", iname)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		path := filepath.Join(webpDir, entry.Name())
		if err := os.Remove(path); err != nil {
			slog.Error("Failed to remove webp file on app disable", "path", path, "error", err)
		}
	}

	// Also check for pushed webp files
	pushedWebpPath := filepath.Join(webpDir, "pushed", fmt.Sprintf("%s.webp", iname))
	if _, err := os.Stat(pushedWebpPath); err == nil {
		if err := os.Remove(pushedWebpPath); err != nil {
			slog.Error("Failed to remove pushed webp file on app disable", "path", pushedWebpPath, "error", err)
		}
	}
}

// isSafePathComponent reports whether name can be joined into a path as a
// single element without escaping the directory it is joined to.
func isSafePathComponent(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return filepath.Base(name) == name
}

func (s *Server) handleDeleteInstallationAPI(w http.ResponseWriter, r *http.Request) {
	installID := filepath.Base(r.PathValue("iname"))

	device := GetDevice(r)

	// First try to find the app by iname (server-generated ID)
	app, err := gorm.G[data.App](s.DB).Where("device_id = ? AND iname = ?", device.ID, installID).First(r.Context())
	if err != nil {
		// If not found by iname, try to find by installationID (stored in path as "pushed:{installationID}")
		app, err = gorm.G[data.App](s.DB).Where("device_id = ? AND path = ?", device.ID, "pushed:"+installID).First(r.Context())
		if err != nil {
			http.Error(w, "App not found", http.StatusNotFound)
			return
		}
	}

	// Delete the app
	if _, err := gorm.G[data.App](s.DB).Where("id = ?", app.ID).Delete(r.Context()); err != nil {
		http.Error(w, "Failed to delete app", http.StatusInternalServerError)
		return
	}

	// Clean up files using the actual iname
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	if err != nil {
		slog.Error("Failed to get device webp directory for app delete cleanup", "device_id", device.ID, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	// Clean up pushed app image if applicable
	if app.Pushed && app.Path != nil && len(*app.Path) > 7 && (*app.Path)[:7] == "pushed:" {
		pushedID := (*app.Path)[7:]
		pushedWebpPath := filepath.Join(webpDir, "pushed", pushedID+".webp")
		if err := os.Remove(pushedWebpPath); err != nil && !os.IsNotExist(err) {
			slog.Error("Failed to remove pushed webp file", "path", pushedWebpPath, "error", err)
		}
	}

	matches, _ := filepath.Glob(filepath.Join(webpDir, fmt.Sprintf("*-%s.webp", app.Iname)))
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

	if err := s.sendRebootCommand(r.Context(), device.ID); err != nil {
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
	SkipBootAnimation  *bool   `json:"skipBootAnimation"`
	PreferIPv6         *bool   `json:"preferIPv6"`
	APMode             *bool   `json:"apMode"`
	SwapColors         *bool   `json:"swapColors"`
	DisableTouch       *bool   `json:"disableTouch"`
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
	if update.SkipBootAnimation != nil {
		payload["skip_boot_animation"] = *update.SkipBootAnimation
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
	if update.DisableTouch != nil {
		payload["disable_touch"] = *update.DisableTouch
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

	if err := s.sendFirmwareSettingsCommand(r.Context(), device.ID, payload); err != nil {
		slog.Error("Failed to send firmware settings command", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

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
