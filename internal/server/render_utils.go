package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/renderer"
	"tronbyt-server/web"
)

func (s *Server) possiblyRender(ctx context.Context, app *data.App, device *data.Device, user *data.User) bool {
	if app.Path == nil || *app.Path == "" {
		return false
	}

	appPath := s.resolveAppPath(*app.Path)
	appBasename := fmt.Sprintf("%s-%s", app.Name, app.Iname)
	webpDir := s.getDeviceWebPDir(device.ID)
	webpPath := filepath.Join(webpDir, fmt.Sprintf("%s.webp", appBasename))

	// 1. Static WebP App
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

	// 2. Pushed App (Pre-rendered)
	if app.Pushed {
		return true
	}

	// 3. Starlark App - Check Interval
	now := time.Now()
	// uinterval is seconds
	if time.Since(app.LastRender) > time.Duration(app.UInterval)*time.Second {
		slog.Info("Rendering app", "app", appBasename)

		// Config
		config := make(map[string]string)
		for k, v := range app.Config {
			if str, ok := v.(string); ok {
				config[k] = str
			} else {
				config[k] = fmt.Sprintf("%v", v)
			}
		}

		// Add default config
		deviceTimezone := device.GetTimezone()
		config["$tz"] = deviceTimezone
		if device.Location.Lat != "" {
			config["$lat"] = device.Location.Lat
		}
		if device.Location.Lng != "" {
			config["$lng"] = device.Location.Lng
		}

		appInterval := app.DisplayTime
		if appInterval == 0 {
			appInterval = device.DefaultInterval
		}

		// Filters
		filters := s.getEffectiveFilters(device, app)

		startTime := time.Now()
		imgBytes, messages, err := renderer.Render(
			ctx,
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
		for _, msg := range messages {
			slog.Info("Render message", "app", appBasename, "message", msg)
		}
		renderDur := time.Since(startTime)

		success := err == nil && len(imgBytes) > 0

		if !success {
			slog.Error("Error rendering app", "app", appBasename, "error", err)
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
	if GetNightModeIsActive(device) && device.NightColorFilter != nil {
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

func (s *Server) resolveAppPath(path string) string {
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.DataDir, path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		slog.Error("Failed to resolve absolute path", "path", path, "error", err)
		return path
	}
	return abs
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
	path := "static/images/default.webp"
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

	brightness := device.Brightness
	if GetNightModeIsActive(device) {
		brightness = device.NightBrightness
	} else if GetDimModeIsActive(device) && device.DimBrightness != nil {
		brightness = *device.DimBrightness
	}
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
