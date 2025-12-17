package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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
// It handles config normalization, timezone/locale injection, dwell time, and filters.
func (s *Server) RenderApp(ctx context.Context, device *data.Device, app *data.App, appPath string, configOverrides map[string]any) ([]byte, []string, error) {
	// Config
	var rawConfig map[string]any
	if configOverrides != nil {
		rawConfig = configOverrides
	} else if app != nil {
		rawConfig = app.Config
	}
	config := normalizeConfig(rawConfig)

	// Timezone & Locale
	var deviceTimezone string
	var locale *string
	supports2x := false

	if device != nil {
		deviceTimezone = device.GetTimezone()
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
	// uinterval is seconds
	if time.Since(app.LastRender) > time.Duration(app.UInterval)*time.Second {
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
		} else {
			// Save WebP
			if err := os.WriteFile(webpPath, imgBytes, 0644); err != nil {
				slog.Error("Failed to write webp", "path", webpPath, "error", err)
			}
		}

		// Update App State in DB
		updates := map[string]any{
			"last_render":       now,
			"last_render_dur":   renderDur,
			"empty_last_render": !success,
			"render_messages":   data.StringSlice(messages),
		}
		s.DB.Model(app).Updates(updates)

		// Update in-memory object (passed pointer)
		app.LastRender = now
		app.LastRenderDur = renderDur
		app.EmptyLastRender = !success
		app.RenderMessages = messages

		// Handle Autopin
		if app.AutoPin && success {
			s.DB.Model(device).Update("pinned_app", app.Iname)
			device.PinnedApp = &app.Iname
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

func normalizeConfig(input map[string]any) map[string]string {
	config := make(map[string]string)
	for k, v := range input {
		if str, ok := v.(string); ok {
			config[k] = str
		} else {
			if b, err := json.Marshal(v); err == nil {
				config[k] = string(b)
			} else {
				config[k] = fmt.Sprintf("%v", v)
			}
		}
	}
	return config
}
