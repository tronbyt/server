package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"log/slog"
	"tronbyt-server/internal/data"
)

// handleNextApp is the handler for GET /v0/devices/{id}/next
func (s *Server) handleNextApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	var device *data.Device
	if d, err := DeviceFromContext(r.Context()); err == nil {
		device = d
	} else if u, err := UserFromContext(r.Context()); err == nil {
		for i := range u.Devices {
			if u.Devices[i].ID == id {
				device = &u.Devices[i]
				break
			}
		}
	} else {
		// Fallback: Fetch from DB directly (No Auth required for device operation)
		var d data.Device
		if err := s.DB.Preload("Apps").First(&d, "id = ?", id).Error; err == nil {
			device = &d
		}
	}

	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	if len(device.Apps) == 0 {
		var reloaded data.Device
		if err := s.DB.Preload("Apps").First(&reloaded, "id = ?", device.ID).Error; err == nil {
			device = &reloaded
		}
	}

	user, _ := UserFromContext(r.Context())
	if user == nil {
		var owner data.User
		s.DB.First(&owner, "username = ?", device.Username)
		user = &owner
	}

	imgData, app, err := s.GetNextAppImage(r.Context(), device, user)
	if err != nil {
		// Send default image if error (or not found)
		s.sendDefaultImage(w, r, device)
		return
	}

	// Send Headers
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("Tronbyt-Brightness", fmt.Sprintf("%d", device.Brightness))

	dwell := device.DefaultInterval
	if app != nil && app.DisplayTime > 0 {
		dwell = app.DisplayTime
	}
	w.Header().Set("Tronbyt-Dwell-Secs", fmt.Sprintf("%d", dwell))

	if _, err := w.Write(imgData); err != nil {
		slog.Error("Failed to write image data to response", "error", err)
		// Log error, but can't change HTTP status after writing headers.
	}
}

func (s *Server) GetNextAppImage(ctx context.Context, device *data.Device, user *data.User) ([]byte, *data.App, error) {
	// 1. Check Pushed Ephemeral Images (__*)
	pushedDir := filepath.Join(s.DataDir, "webp", device.ID, "pushed")
	if entries, err := os.ReadDir(pushedDir); err == nil {
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "__") {
				fullPath := filepath.Join(pushedDir, entry.Name())
				data, err := os.ReadFile(fullPath)
				if err == nil {
					if err := os.Remove(fullPath); err != nil {
						slog.Error("Failed to remove ephemeral image", "path", fullPath, "error", err)
					}
					return data, nil, nil
				}
			}
		}
	}

	// 2. Apps Check
	if len(device.Apps) == 0 {
		return nil, nil, fmt.Errorf("no apps")
	}

	// 3. Brightness Check
	if device.Brightness == 0 {
		return nil, nil, fmt.Errorf("brightness 0")
	}

	// 4. Rotation Logic
	app, nextIndex, err := s.determineNextApp(device, user)
	if err != nil || app == nil {
		return nil, nil, fmt.Errorf("no valid app")
	}

	// 5. Save State
	if nextIndex != device.LastAppIndex {
		if err := s.DB.Model(device).Update("last_app_index", nextIndex).Error; err != nil {
			slog.Error("Failed to update last_app_index", "error", err)
		}
		device.LastAppIndex = nextIndex // Keep in-memory updated

		// Notify Dashboard
		if user != nil {
			s.Broadcaster.Notify("user:" + user.Username)
		}
	}

	// 6. Return Image
	var webpPath string
	if app.Pushed {
		webpPath = filepath.Join(s.getDeviceWebPDir(device.ID), "pushed", app.Iname+".webp")
	} else {
		appBasename := fmt.Sprintf("%s-%s", app.Name, app.Iname)
		webpPath = filepath.Join(s.getDeviceWebPDir(device.ID), fmt.Sprintf("%s.webp", appBasename))
	}

	data, err := os.ReadFile(webpPath)
	return data, app, err
}

func (s *Server) GetCurrentAppImage(ctx context.Context, device *data.Device) ([]byte, *data.App, error) {
	// Re-fetch device with Apps if missing
	if len(device.Apps) == 0 {
		s.DB.Preload("Apps").First(device, "id = ?", device.ID)
	}

	// User
	var user data.User
	s.DB.First(&user, "username = ?", device.Username)

	// Reuse determineNextApp but we want the *current* state.
	// Actually, just serving the app at LastAppIndex is usually enough for "Current App" display.
	// But we need to handle the case where LastAppIndex is invalid or app is disabled.
	// Ideally we run the rotation logic starting at LastAppIndex - 1 (so next is LastAppIndex)?

	// Let's implement a simplified version that just tries to render the app at LastAppIndex.

	apps := make([]data.App, len(device.Apps))
	copy(apps, device.Apps)
	sort.Slice(apps, func(i, j int) bool {
		return apps[i].Order < apps[j].Order
	})
	expanded := createExpandedAppsList(device, apps)

	if len(expanded) == 0 {
		return nil, nil, fmt.Errorf("no apps")
	}

	idx := device.LastAppIndex
	if idx >= len(expanded) {
		idx = 0
	}

	app := &expanded[idx]

	// Return image
	var webpPath string
	if app.Pushed {
		webpPath = filepath.Join(s.getDeviceWebPDir(device.ID), "pushed", app.Iname+".webp")
	} else {
		appBasename := fmt.Sprintf("%s-%s", app.Name, app.Iname)
		webpPath = filepath.Join(s.getDeviceWebPDir(device.ID), fmt.Sprintf("%s.webp", appBasename))
	}

	data, err := os.ReadFile(webpPath)
	return data, app, err
}

func (s *Server) determineNextApp(device *data.Device, user *data.User) (*data.App, int, error) {
	// Sort Apps
	apps := make([]data.App, len(device.Apps))
	copy(apps, device.Apps)
	sort.Slice(apps, func(i, j int) bool {
		return apps[i].Order < apps[j].Order
	})

	// Create Expanded List (with Interstitials)
	expanded := createExpandedAppsList(device, apps)

	lastIndex := device.LastAppIndex

	// Loop to find next valid app
	for i := 0; i < len(expanded)*2; i++ {
		nextIndex := (lastIndex + 1) % len(expanded)
		if len(expanded) == 0 {
			break
		}

		candidate := expanded[nextIndex]

		isPinned := device.PinnedApp != nil && *device.PinnedApp == candidate.Iname
		isNight := GetNightModeIsActive(device) && device.NightModeApp == candidate.Iname
		isInterstitialPos := device.InterstitialEnabled && nextIndex%2 == 1

		shouldDisplay := false

		if isPinned || isNight {
			shouldDisplay = true
		} else if isInterstitialPos {
			shouldDisplay = true
		} else {
			active := candidate.Enabled && IsAppScheduleActive(&candidate, device)
			if active {
				shouldDisplay = true
			}
		}

		if device.InterstitialApp != nil && *device.InterstitialApp == candidate.Iname && !isInterstitialPos {
			if !candidate.Enabled {
				shouldDisplay = false
			}
		}

		if shouldDisplay {
			if s.possiblyRender(context.Background(), &candidate, device, user) && !candidate.EmptyLastRender {
				return &candidate, nextIndex, nil
			}

			if isPinned {
				s.DB.Model(device).Update("pinned_app", nil)
			}
		}

		lastIndex = nextIndex
	}

	return nil, 0, nil
}

func createExpandedAppsList(device *data.Device, apps []data.App) []data.App {
	if !device.InterstitialEnabled || device.InterstitialApp == nil {
		return apps
	}

	interstitialIname := *device.InterstitialApp
	var interstitialApp *data.App

	// Find interstitial app object
	for i := range apps {
		if apps[i].Iname == interstitialIname {
			interstitialApp = &apps[i]
			break
		}
	}

	if interstitialApp == nil {
		return apps
	}

	var expanded []data.App
	for i, app := range apps {
		expanded = append(expanded, app)
		// Add interstitial after each regular app, except the last one
		if i < len(apps)-1 {
			expanded = append(expanded, *interstitialApp)
		}
	}
	return expanded
}
