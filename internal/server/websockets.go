package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"tronbyt-server/internal/data"

	"github.com/gorilla/websocket"
)

type WSMessage struct {
	Queued     *int        `json:"queued"`
	Displaying *int        `json:"displaying"`
	Counter    *int        `json:"counter"`
	ClientInfo *ClientInfo `json:"client_info"`
}

type ClientInfo struct {
	FirmwareVersion string `json:"firmwareVersion"`
	FirmwareType    string `json:"firmwareType"`
	ProtocolVersion *int   `json:"protocolVersion"`
	MACAddress      string `json:"macAddress"`
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
	s.DB.Model(&device).Update("info", data.JSONMap{"protocol_type": "ws"})

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

func (s *Server) wsWriteLoop(ctx context.Context, conn *websocket.Conn, initialDevice *data.Device, user *data.User, ackCh <-chan WSMessage, broadcastCh <-chan struct{}, stopCh <-chan struct{}) {
	for {
		select {
		case <-stopCh:
			return
		default:
		}

		// Reload device to get latest state (protocol version, brightness, etc.)
		var device data.Device
		if err := s.DB.First(&device, "id = ?", initialDevice.ID).Error; err != nil {
			slog.Error("Device gone", "id", initialDevice.ID)
			return
		}

		// 1. Get Next Image
		imgData, app, err := s.GetNextAppImage(ctx, &device, user)
		if err != nil {
			slog.Error("Failed to get next app", "error", err)
			time.Sleep(5 * time.Second)
			continue
		}

		dwell := device.DefaultInterval
		if app != nil && app.DisplayTime > 0 {
			dwell = app.DisplayTime
		}

		// 2. Send Data
		if err := conn.WriteJSON(map[string]int{"dwell_secs": dwell}); err != nil {
			slog.Error("Failed to write dwell_secs WS message", "error", err)
			return
		}
		if err := conn.WriteJSON(map[string]int{"brightness": int(device.Brightness)}); err != nil {
			slog.Error("Failed to write brightness WS message", "error", err)
			return
		}
		if err := conn.WriteMessage(websocket.BinaryMessage, imgData); err != nil {
			return
		}

		// 3. Wait for ACK or Timeout or Interrupt
		var timeoutSec int
		if device.Info.ProtocolVersion != nil {
			// New firmware: wait longer to allow buffering/ack
			timeoutSec = dwell * 2
			if timeoutSec < 25 {
				timeoutSec = 25
			}
		} else {
			// Old firmware: wait exactly dwell time
			timeoutSec = dwell
		}

		timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)

		interrupted := false
		waiting := true

		for waiting {
			select {
			case <-ackCh:
				// Received ACK (Queued or Displaying).
				waiting = false
			case <-broadcastCh:
				// Update available
				interrupted = true
				waiting = false
				if err := conn.WriteJSON(map[string]bool{"immediate": true}); err != nil {
					slog.Error("Failed to write immediate WS message", "error", err)
					return
				}
			case <-timer.C:
				// Timeout
				waiting = false
			case <-stopCh:
				timer.Stop()
				return
			}
		}
		timer.Stop()

		if interrupted {
			continue
		}
	}
}
