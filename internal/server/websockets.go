package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"tronbyt-server/internal/data"

	"github.com/gorilla/websocket"
	"gorm.io/gorm"
)

const (
	// minAckTimeoutSeconds is the minimum time to wait for device ACK.
	// This accounts for the previous app's dwell time, network latency, and processing overhead.
	minAckTimeoutSeconds = 30
)

type WSMessage struct {
	Queued     *int        `json:"queued"`
	Displaying *int        `json:"displaying"`
	Counter    *int        `json:"counter"`
	ClientInfo *ClientInfo `json:"client_info"`
}

type ClientInfo struct {
	FirmwareVersion    string  `json:"firmware_version"`
	FirmwareType       string  `json:"firmware_type"`
	ProtocolVersion    *int    `json:"protocol_version"`
	MACAddress         string  `json:"mac"`
	SSID               *string `json:"ssid"`
	WifiPowerSave      *int    `json:"wifi_power_save"`
	SkipDisplayVersion *bool   `json:"skip_display_version"`
	APMode             *bool   `json:"ap_mode"`
	PreferIPv6         *bool   `json:"prefer_ipv6"`
	SwapColors         *bool   `json:"swap_colors"`
	ImageURL           *string `json:"image_url"`
	Hostname           *string `json:"hostname"`
	SNTPServer         *string `json:"sntp_server"`
	SyslogAddr         *string `json:"syslog_addr"`
}

type WSEvent struct {
	Type     string `json:"type"`
	DeviceID string `json:"device_id,omitempty"`
	AppID    string `json:"app_id,omitempty"`
	Payload  any    `json:"payload,omitempty"`
}

type DeviceCommandMessage struct {
	Payload []byte
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	deviceID := r.PathValue("id")

	device, err := s.reloadDevice(deviceID)
	if err != nil {
		slog.Warn("WS connection rejected", "id", deviceID, "error", err)
		http.Error(w, "Device not found", http.StatusNotFound)
		return
	}

	user, err := gorm.G[data.User](s.DB).Where("username = ?", device.Username).First(r.Context())
	if err != nil {
		slog.Error("User for device not found in WS handler", "username", device.Username, "error", err)
		http.Error(w, "Internal Server Error: device owner not found", http.StatusInternalServerError)
		return
	}

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
		device.Info.ProtocolType = data.ProtocolWS
		if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(r.Context(), "info", data.JSONMap{"protocol_type": data.ProtocolWS}); err != nil {
			slog.Error("Failed to update protocol_type", "error", err)
		}
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
			if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(context.Background(), "last_seen", time.Now()); err != nil {
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

				if msg.ClientInfo.SSID != nil {
					device.Info.SSID = msg.ClientInfo.SSID
				}
				if msg.ClientInfo.WifiPowerSave != nil {
					device.Info.WifiPowerSave = msg.ClientInfo.WifiPowerSave
				}
				if msg.ClientInfo.SkipDisplayVersion != nil {
					device.Info.SkipDisplayVersion = msg.ClientInfo.SkipDisplayVersion
				}
				if msg.ClientInfo.APMode != nil {
					device.Info.APMode = msg.ClientInfo.APMode
				}
				if msg.ClientInfo.PreferIPv6 != nil {
					device.Info.PreferIPv6 = msg.ClientInfo.PreferIPv6
				}
				if msg.ClientInfo.SwapColors != nil {
					device.Info.SwapColors = msg.ClientInfo.SwapColors
				}
				if msg.ClientInfo.ImageURL != nil {
					device.Info.ImageURL = msg.ClientInfo.ImageURL
				}
				if msg.ClientInfo.Hostname != nil {
					device.Info.Hostname = msg.ClientInfo.Hostname
				}
				if msg.ClientInfo.SNTPServer != nil {
					device.Info.SNTPServer = msg.ClientInfo.SNTPServer
				}
				if msg.ClientInfo.SyslogAddr != nil {
					device.Info.SyslogAddr = msg.ClientInfo.SyslogAddr
				}

				if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(context.Background(), "info", device.Info); err != nil {
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
					if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(context.Background(), "info", device.Info); err != nil {
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

func (s *Server) wsWriteLoop(ctx context.Context, conn *websocket.Conn, initialDevice *data.Device, user *data.User, ackCh <-chan WSMessage, broadcastCh <-chan any, stopCh <-chan struct{}) {
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
			// Device may delay ACK until previous app completes its dwell time.
			// Wait at least minAckTimeoutSeconds OR 2x the dwell time, whichever is greater.
			timeoutSec = max(dwell*2, minAckTimeoutSeconds)
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
					// The firmware sends a sequential counter (1, 2, 3...) in the 'displaying' field,
					// not the App ID we sent. Since we can't verify which specific app is displaying,
					// we accept any ACK as confirmation that the device received and processed our message.
					waiting = false

					// Update DisplayingApp confirmation in DB
					if app != nil {
						slog.Debug("Received ACK, updating DisplayingApp", "app", app.Iname, "device", device.ID)
						// Only now do we update the database that the device is truly displaying this app.
						if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(ctx, "displaying_app", app.Iname); err != nil {
							slog.Error("Failed to update displaying_app", "device", device.ID, "error", err)
						}
						// Notify Dashboard
						s.notifyDashboard(user.Username, WSEvent{Type: "image_updated", DeviceID: device.ID})
					} else {
						slog.Debug("Received ACK for default or pushed image (no app context)", "device", device.ID)
					}
				}
				// If just Queued, we keep waiting for Displaying.
			case val := <-broadcastCh:
				// Update available (Reload device first)
				reloaded, err := gorm.G[data.Device](s.DB).Preload("Apps", nil).Where("id = ?", initialDevice.ID).First(ctx)
				if err != nil {
					slog.Error("Device gone", "id", initialDevice.ID)
					return
				}
				device = reloaded

				var isCommand bool
				var isImage bool
				var cmdPayload []byte
				var imgData []byte

				switch v := val.(type) {
				case DeviceCommandMessage:
					isCommand = true
					cmdPayload = v.Payload
				case []byte:
					if len(v) > 0 {
						isImage = true
						imgData = v
					}
				}

				if isCommand {
					// It's a command, send it directly as JSON (TextMessage)
					if err := conn.WriteMessage(websocket.TextMessage, cmdPayload); err != nil {
						slog.Error("Failed to write command to WS", "error", err)
						return
					}
					// Don't interrupt the current app for settings changes, unless it's a reboot (which the device handles)
					continue
				}

				if isImage {
					// Pushed Image: Interrupt and send
					pendingImage = imgData
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
		case val := <-ch:
			var data []byte
			if b, ok := val.([]byte); ok {
				data = b
			}

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
	device, err := gorm.G[data.Device](s.DB).Preload("Apps", nil).Where("id = ?", deviceID).First(context.Background())
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("device not found: %s", deviceID)
		}
		return nil, fmt.Errorf("reload device: %w", err)
	}
	return &device, nil
}
