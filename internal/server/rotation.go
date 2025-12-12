package server

import (
	"context"
	"fmt"

	"os"
	"path/filepath"
	"sort"
	"strings"

	"time"

	"log/slog"
	"tronbyt-server/internal/data"
	"tronbyt-server/web"
)

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

	// Helper to return default image
	getDefaultImage := func() ([]byte, *data.App, error) {
		data, err := web.Assets.ReadFile("static/images/default.webp")
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read default image: %w", err)
		}
		return data, nil, nil
	}

	// 2. Apps Check
	if len(device.Apps) == 0 {
		return getDefaultImage()
	}

	// 3. Brightness Check
	if device.Brightness == 0 {
		slog.Debug("Brightness is 0, returning default image")
		return getDefaultImage()
	}

	// 4. Rotation Logic
	app, nextIndex, err := s.determineNextApp(ctx, device, user)
	if err != nil || app == nil {
		// No valid app found (e.g. all disabled or scheduled out)
		return getDefaultImage()
	}

	// 5. Save State
	updates := map[string]any{}
	if nextIndex != device.LastAppIndex {
		updates["last_app_index"] = nextIndex
		device.LastAppIndex = nextIndex // Keep in-memory updated
	}
	updates["last_seen"] = time.Now()
	// Update LastSeen in memory if needed, though mostly for DB
	now := time.Now()
	device.LastSeen = &now

	if err := s.DB.Model(device).Updates(updates).Error; err != nil {
		slog.Error("Failed to update device state (last_app_index/last_seen)", "error", err)
	}

	// Notify Dashboard that the device has updated (new app or new render)
	if user != nil {
		s.notifyDashboard(user.Username, WSEvent{Type: "image_updated", DeviceID: device.ID})
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
	// If reading the specific app image fails, fall back to default
	if err != nil {
		slog.Error("Failed to read app image", "path", webpPath, "error", err)
		return getDefaultImage()
	}
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

func (s *Server) determineNextApp(ctx context.Context, device *data.Device, user *data.User) (*data.App, int, error) {
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
		isNight := device.GetNightModeIsActive() && device.NightModeApp == candidate.Iname
		isInterstitialPos := device.InterstitialEnabled && nextIndex%2 == 1

		shouldDisplay := false

		if isPinned || isNight {
			shouldDisplay = true
		} else if isInterstitialPos {
			shouldDisplay = true
			// Interstitial Logic: Skip if previous regular app (at index-1) is skipped
			// Note: expanded list is always [App, Interstitial, App, Interstitial...]
			// So an interstitial at index i corresponds to App at i-1.
			if nextIndex > 0 {
				prevApp := expanded[nextIndex-1]
				prevActive := prevApp.Enabled && IsAppScheduleActive(&prevApp, device)
				if !prevActive {
					shouldDisplay = false
				}
			}
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
			if s.possiblyRender(ctx, &candidate, device, user) && !candidate.EmptyLastRender {
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

	expanded := make([]data.App, 0, len(device.Apps)*10) // Pre-allocate to a reasonable size
	for i, app := range apps {
		expanded = append(expanded, app)
		// Add interstitial after each regular app, except the last one
		if i < len(apps)-1 {
			expanded = append(expanded, *interstitialApp)
		}
	}
	return expanded
}
