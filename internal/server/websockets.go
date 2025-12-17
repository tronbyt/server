package server

import (
	"context"
	"fmt"
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
	FirmwareVersion string `json:"firmware_version"`
	FirmwareType    string `json:"firmware_type"`
	ProtocolVersion *int   `json:"protocol_version"`
	MACAddress      string `json:"mac"`
}

type WSEvent struct {
	Type     string `json:"type"`
	DeviceID string `json:"device_id,omitempty"`
	AppID    string `json:"app_id,omitempty"`
	Payload  any    `json:"payload,omitempty"`
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")

	device, err := s.reloadDevice(deviceID)
	if err != nil {
		slog.Warn("WS connection rejected: device not found", "id", deviceID, "error", err)
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

	// Update protocol type if different from current
	if device.Info.ProtocolType != data.ProtocolWS {
		slog.Info("Updating protocol_type to WS on connect", "device", deviceID)
		s.DB.Model(&device).Update("info", data.JSONMap{"protocol_type": data.ProtocolWS})
	}
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

			// Update LastSeen
			if err := s.DB.Model(&device).Update("last_seen", time.Now()).Error; err != nil {
				slog.Error("Failed to update last_seen", "error", err)
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

			if msg.Queued != nil {
				// If we get a queued message, it's a new firmware device.
				// Update protocol version if not set.
				if device.Info.ProtocolVersion == nil {
					slog.Info("First 'queued' message, setting protocol_version to 1", "device", deviceID)
					newVersion := 1
					device.Info.ProtocolVersion = &newVersion
					if err := s.DB.Model(&device).Update("info", device.Info).Error; err != nil {
						slog.Error("Failed to update device info (protocol version)", "error", err)
					}
				}
			}

			if msg.Displaying != nil || msg.Counter != nil {
				select {
				case ackCh <- msg:
				default:
				}
			}
		}
	}()

	s.wsWriteLoop(r.Context(), conn, device, &user, ackCh, ch, stopCh)
}

func (s *Server) wsWriteLoop(ctx context.Context, conn *websocket.Conn, initialDevice *data.Device, user *data.User, ackCh <-chan WSMessage, broadcastCh <-chan []byte, stopCh <-chan struct{}) {
	var pendingImage []byte
	device := *initialDevice
	lastSentBrightness := -1
	sendImmediate := false

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		// Calculate effective brightness
		effectiveBrightness := device.GetEffectiveBrightness()

		// 1. Get Next Image
		var imgData []byte
		var app *data.App
		var err error

		if pendingImage != nil {
			imgData = pendingImage
			pendingImage = nil
		} else {
			imgData, app, err = s.GetNextAppImage(ctx, &device, user)
			if err != nil {
				slog.Error("Failed to get next app", "error", err)

				// Wait for update or timeout before retrying
				timer := time.NewTimer(5 * time.Second)
				select {
				case <-broadcastCh:
					// Update available - reload device
					reloadedDevice, err := s.reloadDevice(initialDevice.ID)
					if err != nil {
						slog.Error("Device gone", "id", initialDevice.ID, "error", err)
						return
					}
					device = *reloadedDevice
				case <-timer.C:
					// Timeout - reload device just in case we missed something
					reloadedDevice, err := s.reloadDevice(initialDevice.ID)
					if err != nil {
						slog.Error("Device gone", "id", initialDevice.ID, "error", err)
						return
					}
					device = *reloadedDevice
				case <-stopCh:
					timer.Stop()
					return
				}
				timer.Stop()
				continue
			}
		}

		dwell := device.GetEffectiveDwellTime(app)

		// Drain ackCh to remove any stale ACKs directly before sending
	drainLoop:
		for {
			select {
			case <-ackCh:
			default:
				break drainLoop
			}
		}

		// 2. Send Data
		// Send Dwell only, sequence ID is not used by firmware
		if err := conn.WriteJSON(map[string]any{"dwell_secs": dwell}); err != nil {
			slog.Error("Failed to write metadata WS message", "error", err)
			return
		}

		// Only send brightness if changed
		if effectiveBrightness != lastSentBrightness {
			if err := conn.WriteJSON(map[string]int{"brightness": effectiveBrightness}); err != nil {
				slog.Error("Failed to write brightness WS message", "error", err)
				return
			}
			lastSentBrightness = effectiveBrightness
		}

		if err := conn.WriteMessage(websocket.BinaryMessage, imgData); err != nil {
			return
		}

		if sendImmediate {
			if err := conn.WriteJSON(map[string]bool{"immediate": true}); err != nil {
				slog.Error("Failed to write immediate WS message", "error", err)
				return
			}
			sendImmediate = false
		}

		// 3. Wait for ACK or Timeout or Interrupt
		var timeoutSec int
		if device.Info.ProtocolVersion != nil {
			// New firmware: wait longer to allow buffering/ack
			timeoutSec = max(dwell*2, 25)
		} else {
			// Old firmware: wait exactly dwell time
			timeoutSec = dwell
		}

		timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)

		interrupted := false
		waiting := true

		for waiting {
			select {
			case msg := <-ackCh:
				// Received ACK
				if msg.Displaying != nil || msg.Counter != nil {
					waiting = false

					// Update Displaying App confirmation in DB
					if app != nil {
						// slog.Info("Received ACK, updating DisplayingApp", "app", app.Iname, "device", device.ID)
						// Only now do we update the database that the device is truly displaying this app.
						if err := s.DB.Model(&data.Device{ID: device.ID}).Update("displaying_app", app.Iname).Error; err != nil {
							slog.Error("Failed to update displaying_app", "device", device.ID, "error", err)
						}
						// Notify Dashboard
						s.notifyDashboard(user.Username, WSEvent{Type: "image_updated", DeviceID: device.ID})
					} else {
						// slog.Info("Received ACK but app is nil (maybe default image)", "device", device.ID)
					}
				}
				// If just Queued, we keep waiting for Displaying.
			case data := <-broadcastCh:
				// Update available (Reload device first)
				if err := s.DB.Preload("Apps").First(&device, "id = ?", initialDevice.ID).Error; err != nil {
					slog.Error("Device gone", "id", initialDevice.ID)
					return
				}

				if len(data) > 0 {
					// Pushed Image: Interrupt and send
					pendingImage = data
					interrupted = true
					waiting = false
					sendImmediate = true
				} else {
					// State Change: Update Brightness immediately, but don't interrupt current app
					newBrightness := device.GetEffectiveBrightness()

					if newBrightness != lastSentBrightness {
						if err := conn.WriteJSON(map[string]int{"brightness": newBrightness}); err != nil {
							slog.Error("Failed to write brightness WS message", "error", err)
							return
						}
						lastSentBrightness = newBrightness
					}
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

func (s *Server) handleDashboardWS(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	username := user.Username

	conn, err := s.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Dashboard WS upgrade failed", "error", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			slog.Error("Failed to close Dashboard WS connection", "error", err)
		}
	}()

	slog.Debug("Dashboard WS Connected", "username", username)

	// Subscribe to user-specific updates
	ch := s.Broadcaster.Subscribe("user:" + username)
	defer s.Broadcaster.Unsubscribe("user:"+username, ch)

	done := make(chan struct{})

	// Read loop (handle ping/pong/close)
	go func() {
		defer close(done)
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					slog.Info("Dashboard WS read error, disconnecting", "username", username, "error", err)
				}
				return
			}
		}
	}()

	// Write loop
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case data := <-ch:
			// Forward the event data (JSON) to the client
			if len(data) == 0 {
				// Fallback for legacy calls sending nil: trigger generic refresh
				data = []byte(`{"type": "refresh"}`)
			}
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				slog.Error("Failed to write message to Dashboard WS", "username", username, "error", err)
				return
			}
		case <-ticker.C:
			// Keep-alive ping
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				slog.Error("Failed to send Dashboard WS ping", "username", username, "error", err)
				return
			}
		}
	}
}

func (s *Server) SetupWebsocketRoutes() {
	s.Router.HandleFunc("GET /{id}/ws", s.handleWS)
	s.Router.HandleFunc("GET /ws", s.RequireLogin(s.handleDashboardWS))
}

func (s *Server) reloadDevice(deviceID string) (*data.Device, error) {
	var device data.Device
	if err := s.DB.Preload("Apps").First(&device, "id = ?", deviceID).Error; err != nil {
		return nil, fmt.Errorf("device gone: %w", err)
	}
	return &device, nil
}
