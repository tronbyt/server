package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"tronbyt-server/internal/data"

	"gorm.io/gorm"
)

// handleNextApp is the handler for GET /{id}/next.
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
		d, err := gorm.G[data.Device](s.DB).Preload("Apps", nil).Where("id = ?", id).First(r.Context())
		if err == nil {
			device = &d
		}
	}

	if device == nil {
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	if len(device.Apps) == 0 {
		reloaded, err := gorm.G[data.Device](s.DB).Preload("Apps", nil).Where("id = ?", device.ID).First(r.Context())
		if err == nil {
			device = &reloaded
		}
	}

	user, _ := UserFromContext(r.Context())
	if user == nil {
		owner, err := gorm.G[data.User](s.DB).Where("username = ?", device.Username).First(r.Context())
		if err != nil {
			slog.Error("Failed to find device owner", "username", device.Username, "error", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		user = &owner
	}

	// Update device info if needed
	// Device info updates are best-effort and don't require locking
	updated := false
	if device.Info.ProtocolType != data.ProtocolHTTP {
		slog.Debug("Updating protocol_type to HTTP on /next request", "device", device.ID)
		device.Info.ProtocolType = data.ProtocolHTTP
		updated = true
	}

	// Check for firmware version header
	if fwVersion := r.Header.Get("X-Firmware-Version"); fwVersion != "" {
		if device.Info.FirmwareVersion != fwVersion {
			slog.Debug("Updating firmware_version on /next request", "device", device.ID, "version", fwVersion)
			device.Info.FirmwareVersion = fwVersion
			updated = true
		}
	}

	if updated {
		if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(r.Context(), "info", device.Info); err != nil {
			slog.Error("Failed to update device info", "device", device.ID, "error", err)
		}
	}

	imgData, app, err := s.GetNextAppImage(r.Context(), device, user)
	if err != nil {
		// Send default image if error (or not found)
		slog.Error("Failed to get next app image", "device", device.ID, "error", err)
		s.sendDefaultImage(w, r, device)
		return
	}

	// For HTTP devices, we assume "Sent" equals "Displaying" (or roughly so).
	// We update DisplayingApp here so the Preview uses the explicit field instead of fallback.
	if app != nil {
		appIname := app.Iname
		s.WriteQueue.ExecuteAsync(func(db *gorm.DB) error {
			_, err := gorm.G[data.Device](db).Where("id = ?", device.ID).Update(context.Background(), "displaying_app", appIname)
			if err != nil {
				slog.Error("Failed to update displaying_app for HTTP device", "device", device.ID, "error", err)
			}
			return err
		})
	}

	// Send Headers
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")

	// Check for Pending Update
	if updateURL := device.PendingUpdateURL; updateURL != "" {
		slog.Info("Sending OTA update header", "device", device.ID, "url", updateURL)
		w.Header().Set("Tronbyt-OTA-URL", updateURL)

		// Clear pending update using write queue to avoid contention
		s.WriteQueue.ExecuteAsync(func(db *gorm.DB) error {
			_, err := gorm.G[data.Device](db).Where("id = ?", device.ID).Update(context.Background(), "pending_update_url", "")
			if err != nil {
				slog.Error("Failed to clear pending update", "error", err)
			}
			return err
		})
		device.PendingUpdateURL = ""
	}

	// Determine Brightness
	brightness := device.GetEffectiveBrightness()
	w.Header().Set("Tronbyt-Brightness", fmt.Sprintf("%d", brightness))

	dwell := device.GetEffectiveDwellTime(app)
	w.Header().Set("Tronbyt-Dwell-Secs", fmt.Sprintf("%d", dwell))

	if _, err := w.Write(imgData); err != nil {
		slog.Error("Failed to write image data to response", "error", err)
		// Log error, but can't change HTTP status after writing headers.
	}
}
