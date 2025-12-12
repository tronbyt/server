package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"tronbyt-server/internal/data"
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

	// Update protocol type if different from current
	if device.Info.ProtocolType != data.ProtocolHTTP {
		slog.Info("Updating protocol_type to HTTP on /next request", "device", device.ID)
		s.DB.Model(device).Update("info", data.JSONMap{"protocol_type": data.ProtocolHTTP})
		// Update in-memory state so subsequent calls (like GetNextAppImage logging) reflect it if needed
		device.Info.ProtocolType = data.ProtocolHTTP
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
