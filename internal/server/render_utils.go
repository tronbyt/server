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

func (s *Server) getDeviceWebPDir(deviceID string) string {
	path := filepath.Join(s.DataDir, "webp", deviceID)
	if err := os.MkdirAll(path, 0755); err != nil {
		slog.Error("Failed to create device webp directory", "path", path, "error", err)
		// Non-fatal, continue.
	}
	return path
}

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
	now := time.Now().Unix()
	// uinterval is minutes
	if now-app.LastRender > int64(app.UInterval*60) {
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
		config["$tz"] = getDeviceTimezone(device)
		if device.Location.Lat != "" {
			config["$lat"] = device.Location.Lat
		}
		if device.Location.Lng != "" {
			config["$lng"] = device.Location.Lng
		}

		// Read Script
		script, err := os.ReadFile(appPath)
		if err != nil {
			slog.Error("Failed to read script", "path", appPath, "error", err)
			return false // Don't return false, just skip rendering? No, failed.
		}

		startTime := time.Now()
		imgBytes, err := renderer.Render(ctx, script, config, 64, 32)
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
			"last_render_dur":   renderDur.Nanoseconds(),
			"empty_last_render": !success,
		}
		s.DB.Model(app).Updates(updates)

		// Update in-memory object (passed pointer)
		app.LastRender = now
		app.LastRenderDur = renderDur.Nanoseconds()
		app.EmptyLastRender = !success

		// Handle Autopin
		if app.AutoPin && success {
			s.DB.Model(device).Update("pinned_app", app.Iname)
			device.PinnedApp = &app.Iname
		}

		return success
	}

	return true // Not time to render yet, assume existing is fine
}

func (s *Server) resolveAppPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(s.DataDir, path)
}

func getDeviceTimezone(device *data.Device) string {
	if device.Timezone != nil && *device.Timezone != "" {
		return *device.Timezone
	}
	if device.Location.Timezone != "" {
		return device.Location.Timezone
	}
	return "UTC" // Default
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
