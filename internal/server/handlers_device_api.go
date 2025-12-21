package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"tronbyt-server/internal/data"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

	// Update device info if needed
	// We use a transaction with locking to avoid race conditions with other requests
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		var freshDevice data.Device
		// Lock the row to ensure we read the latest state and no one else updates it
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&freshDevice, "id = ?", device.ID).Error; err != nil {
			return err
		}

		updated := false
		if freshDevice.Info.ProtocolType != data.ProtocolHTTP {
			slog.Debug("Updating protocol_type to HTTP on /next request", "device", device.ID)
			freshDevice.Info.ProtocolType = data.ProtocolHTTP
			updated = true
		}

		// Check for firmware version header
		if fwVersion := r.Header.Get("X-Firmware-Version"); fwVersion != "" {
			if freshDevice.Info.FirmwareVersion != fwVersion {
				slog.Debug("Updating firmware_version on /next request", "device", device.ID, "version", fwVersion)
				freshDevice.Info.FirmwareVersion = fwVersion
				updated = true
			}
		}

		if updated {
			if err := tx.Model(&freshDevice).Update("info", freshDevice.Info).Error; err != nil {
				return err
			}
			// Update the in-memory device object so subsequent logic uses the new values
			device.Info = freshDevice.Info
		}
		return nil
	})

	if err != nil {
		slog.Error("Failed to update device info transaction", "device", device.ID, "error", err)
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
		if err := s.DB.Model(&data.Device{ID: device.ID}).Update("displaying_app", app.Iname).Error; err != nil {
			slog.Error("Failed to update displaying_app for HTTP device", "device", device.ID, "error", err)
		}
	}

	// Send Headers
	w.Header().Set("Content-Type", "image/webp")
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")

	// Check for Pending Update
	if updateURL := device.PendingUpdateURL; updateURL != "" {
		slog.Info("Sending OTA update header", "device", device.ID, "url", updateURL)
		w.Header().Set("Tronbyt-OTA-URL", updateURL)

		// Clear pending update
		if err := s.DB.Model(device).Update("pending_update_url", "").Error; err != nil {
			slog.Error("Failed to clear pending update", "error", err)
		} else {
			device.PendingUpdateURL = ""
		}
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
