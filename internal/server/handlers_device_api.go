package server

import (
	"fmt"
	"log/slog"
	"net/http"

	"tronbyt-server/internal/data"

	"github.com/gorilla/websocket"
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

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")

	var device data.Device
	if err := s.DB.Preload("Apps").First(&device, "id = ?", deviceID).Error; err != nil {
		slog.Warn("WS connection rejected: device not found", "id", deviceID)
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	var user data.User
	s.DB.First(&user, "username = ?", device.Username)

	conn, err := s.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("WS upgrade failed", "error", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("Failed to close WS connection", "error", err)
		}
	}()

	slog.Info("WS Connected", "device", deviceID)

	// Update protocol type
	s.DB.Model(&device).Update("info", data.JSONMap{"protocol_type": data.ProtocolWS})

	ch := s.Broadcaster.Subscribe(deviceID)
	defer s.Broadcaster.Unsubscribe(deviceID, ch)

	ackCh := make(chan WSMessage, 10)
	stopCh := make(chan struct{})

	// Read loop to handle ping/pong/close and client messages
	go func() {
		defer close(stopCh)
		for {
			var msg WSMessage
			if err := conn.ReadJSON(&msg); err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					slog.Info("WS closed unexpectedly", "error", err)
				}
				return
			}

			// Handle Message
			if msg.ClientInfo != nil {
				// Update Device Info
				device.Info.FirmwareVersion = msg.ClientInfo.FirmwareVersion
				device.Info.FirmwareType = msg.ClientInfo.FirmwareType
				if msg.ClientInfo.ProtocolVersion != nil {
					device.Info.ProtocolVersion = msg.ClientInfo.ProtocolVersion
				}
				device.Info.MACAddress = msg.ClientInfo.MACAddress

				if err := s.DB.Model(&device).Update("info", device.Info).Error; err != nil {
					slog.Error("Failed to update device info", "error", err)
				}
			}

			if msg.Queued != nil || msg.Displaying != nil {
				select {
				case ackCh <- msg:
				default:
				}
			}
		}
	}()

	s.wsWriteLoop(r.Context(), conn, &device, &user, ackCh, ch, stopCh)
}
