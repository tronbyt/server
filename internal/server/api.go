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

	"tronbyt-server/internal/data"
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

// PushData represents the data for pushing an image to a device.
type PushData struct {
	InstallationID    string `json:"installationID"`
	InstallationIDAlt string `json:"installationId"`
	Image             string `json:"image"`
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
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

// getDeviceWebpDir is a helper to get device webp directory (from server.go).
func (s *Server) getDeviceWebPDir(deviceID string) string {
	path := filepath.Join(s.DataDir, "webp", deviceID)
	if err := os.MkdirAll(path, 0755); err != nil {
		slog.Error("Failed to create device webp directory", "path", path, "error", err)
		// Non-fatal, continue.
	}
	return path
}

func (s *Server) SetupAPIRoutes() {
	// API v0 Group - authenticated with Middleware
	s.Router.Handle("GET /v0/devices/{id}", s.APIAuthMiddleware(http.HandlerFunc(s.handleGetDevice)))
	s.Router.Handle("POST /v0/devices/{id}/push", s.APIAuthMiddleware(http.HandlerFunc(s.handlePush)))
	s.Router.Handle("GET /v0/devices/{id}/installations", s.APIAuthMiddleware(http.HandlerFunc(s.handleListInstallations)))
	s.Router.Handle("PATCH /v0/devices/{id}", s.APIAuthMiddleware(http.HandlerFunc(s.handlePatchDevice)))
	s.Router.Handle("PATCH /v0/devices/{id}/installations/{iname}", s.APIAuthMiddleware(http.HandlerFunc(s.handlePatchInstallation)))
	s.Router.Handle("DELETE /v0/devices/{id}/installations/{iname}", s.APIAuthMiddleware(http.HandlerFunc(s.handleDeleteInstallationAPI)))

	s.Router.HandleFunc("GET /dots", s.handleDots)
}
