package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"strings"
	"time"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/renderer"
	"tronbyt-server/web"

	securejoin "github.com/cyphar/filepath-securejoin"
)

// RenderApp consolidates the logic for rendering an app for a device.
// It handles config overrides, timezone/locale injection, dwell time, and filters.
func (s *Server) RenderApp(ctx context.Context, device *data.Device, app *data.App, appPath string, configOverrides map[string]any) ([]byte, []string, error) {
	// Config
	var config map[string]any
	switch {
	case configOverrides != nil:
		config = configOverrides
	case app != nil && app.Config != nil:
		config = maps.Clone(app.Config)
	default:
		config = make(map[string]any)
	}

	// Timezone & Locale
	var deviceTimezone string
	var locale *string
	supports2x := false

	if device != nil {
		deviceTimezone = device.GetTimezone()
		// The legacy "$tz" config variable should eventually be removed.
		// The system apps already use `time.tz()`, but users' custom apps might not.
		config["$tz"] = deviceTimezone
		locale = device.Locale
		supports2x = device.Type.Supports2x()
	}

	// Dwell Time
	var appInterval int
	if device != nil {
		appInterval = device.GetEffectiveDwellTime(app)
	} else {
		appInterval = 15 // Default fallback if no device context
	}

	// Filters
	var filters []string
	if device != nil {
		filters = s.getEffectiveFilters(device, app)
	}

	return renderer.Render(
		ctx,
		appPath,
		config,
		64, 32,
		time.Duration(appInterval)*time.Second,
		30*time.Second,
		true,
		supports2x,
		&deviceTimezone,
		locale,
		filters,
	)
}

func (s *Server) possiblyRender(ctx context.Context, app *data.App, device *data.Device, user *data.User) bool {
	// 1. Pushed App (Pre-rendered)
	if app.Pushed {
		return true
	}

	if app.Path == nil || *app.Path == "" {
		return false
	}

	appPath, err := securejoin.SecureJoin(s.DataDir, *app.Path)
	if err != nil {
		slog.Error("Failed to resolve app path", "path", *app.Path, "error", err)
		return false
	}
	appBasename := fmt.Sprintf("%s-%s", app.Name, app.Iname)
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	if err != nil {
		slog.Error("Failed to get device webp directory for rendering", "device_id", device.ID, "error", err)
		return false
	}
	webpPath, err := securejoin.SecureJoin(webpDir, fmt.Sprintf("%s.webp", appBasename))
	if err != nil {
		slog.Error("Path traversal attempt in webp path", "app", appBasename, "error", err)
		return false
	}

	// 2. Static WebP App
	if strings.HasSuffix(strings.ToLower(*app.Path), ".webp") {
		if _, err := os.Stat(webpPath); os.IsNotExist(err) {
			// Copy from source
			if _, err := os.Stat(appPath); err == nil {
				if err := copyFile(appPath, webpPath); err != nil {
					slog.Error("Failed to copy static webp file", "src", appPath, "dst", webpPath, "error", err)
					return false
				}
			} else {
				slog.Warn("Source WebP not found", "path", appPath)
				return false
			}
		}
		return true // Exists
	}

	// 3. Starlark App - Check Interval
	now := time.Now()
	// uinterval is minutes
	if time.Since(app.LastRender) > time.Duration(app.UInterval)*time.Minute {
		slog.Info("Rendering app", "app", appBasename)

		startTime := time.Now()
		imgBytes, messages, err := s.RenderApp(ctx, device, app, appPath, nil)
		renderDur := time.Since(startTime)

		for _, msg := range messages {
			slog.Debug("Render message", "app", appBasename, "message", msg)
		}

		empty := len(imgBytes) == 0
		success := err == nil && !empty

		if err != nil {
			slog.Error("Error rendering app", "app", appBasename, "error", err)
		} else if empty {
			slog.Debug("No output from app", "app", appBasename)
		}

		// Update App State in DB - This is our atomic check-and-update.
		// If the app was deleted while we were rendering, RowsAffected will be 0.
		updates := map[string]any{
			"last_render":       now,
			"last_render_dur":   renderDur,
			"empty_last_render": !success,
			"render_messages":   data.StringSlice(messages),
		}
		if success {
			updates["last_successful_render"] = now
		}
		result := s.DB.Model(&data.App{}).Where("id = ?", app.ID).Updates(updates)
		if result.Error != nil {
			slog.Error("Failed to update app state in DB", "app", appBasename, "error", result.Error)
			return false
		}

		if result.RowsAffected == 0 {
			slog.Info("App no longer exists in DB, aborting", "app", appBasename)
			return false
		}

		if success {
			// Save WebP
			if err := os.WriteFile(webpPath, imgBytes, 0644); err != nil {
				slog.Error("Failed to write webp", "path", webpPath, "error", err)
			}
		}

		// Update in-memory object (passed pointer)
		app.LastRender = now
		if success {
			app.LastSuccessfulRender = &now
		}
		app.LastRenderDur = renderDur
		app.EmptyLastRender = !success
		app.RenderMessages = messages

		// Handle Autopin
		if app.AutoPin && success {
			s.DB.Model(device).Update("pinned_app", app.Iname)
			device.PinnedApp = &app.Iname

			// Notify Dashboard
			s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})
		}

		return success
	}

	return true // Not time to render yet, assume existing is fine
}

func (s *Server) getEffectiveFilters(device *data.Device, app *data.App) []string {
	var filters []string

	// Determine base device filter
	var deviceFilter data.ColorFilter
	if device.GetNightModeIsActive() && device.NightColorFilter != nil {
		deviceFilter = *device.NightColorFilter
	} else if device.ColorFilter != nil {
		deviceFilter = *device.ColorFilter
	} else {
		deviceFilter = data.ColorFilterNone
	}

	appFilter := data.ColorFilterInherit
	if app != nil && app.ColorFilter != nil {
		appFilter = *app.ColorFilter
	}

	if appFilter != data.ColorFilterInherit {
		if appFilter != data.ColorFilterNone {
			filters = append(filters, string(appFilter))
		}
	} else {
		// Inherit from device
		if deviceFilter != data.ColorFilterNone {
			filters = append(filters, string(deviceFilter))
		}
	}
	return filters
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() {
		if err := in.Close(); err != nil {
			slog.Error("Failed to close source file", "error", err)
		}
	}()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() {
		if err := out.Close(); err != nil {
			slog.Error("Failed to close destination file", "error", err)
		}
	}()

	_, err = io.Copy(out, in)
	return err
}

func (s *Server) sendDefaultImage(w http.ResponseWriter, r *http.Request, device *data.Device) {
	// Fallback if main image retrieval fails
	path := "static/images/default.webp"

	// Get default image from embedded assets
	f, err := web.Assets.Open(path)
	if err != nil {
		slog.Error("Failed to open default image from assets", "error", err)
		http.Error(w, "Image not found", http.StatusNotFound)
		return
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("Failed to close default image file", "error", err)
		}
	}()

	stat, _ := f.Stat()

	// Headers
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")

	brightness := device.GetEffectiveBrightness()
	w.Header().Set("Tronbyt-Brightness", fmt.Sprintf("%d", brightness))

	dwell := device.DefaultInterval
	w.Header().Set("Tronbyt-Dwell-Secs", fmt.Sprintf("%d", dwell))

	if rs, ok := f.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "default.webp", stat.ModTime(), rs)
	} else {
		slog.Error("Embedded file does not implement ReadSeeker")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}
