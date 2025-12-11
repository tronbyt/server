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

type WSEvent struct {
	Type     string `json:"type"`
	DeviceID string `json:"device_id,omitempty"`
	AppID    string `json:"app_id,omitempty"`
	Payload  any    `json:"payload,omitempty"`
}

func (s *Server) wsWriteLoop(ctx context.Context, conn *websocket.Conn, initialDevice *data.Device, user *data.User, ackCh <-chan WSMessage, broadcastCh <-chan []byte, stopCh <-chan struct{}) {
	var pendingImage []byte

	for {
		select {
		case <-stopCh:
			return
		default:
		}

		// Reload device to get latest state (protocol version, brightness, etc.)
		var device data.Device
		if err := s.DB.Preload("Apps").First(&device, "id = ?", initialDevice.ID).Error; err != nil {
			slog.Error("Device gone", "id", initialDevice.ID)
			return
		}

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
				time.Sleep(5 * time.Second)
				continue
			}
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
			case <-ackCh:
				// Received ACK (Queued or Displaying).
				waiting = false
			case data := <-broadcastCh:
				// Update available
				interrupted = true
				waiting = false
				if len(data) > 0 {
					pendingImage = data
				}
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
