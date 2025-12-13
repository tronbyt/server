package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

func TestWebsockets_Client(t *testing.T) {
	s := newTestServerAPI(t)

	// Create a test device
	deviceID := "ws_test_device"
	device := data.Device{
		ID:              deviceID,
		Username:        "testuser", // Matches user created in newTestServerAPI
		Name:            "WS Test Device",
		APIKey:          "ws_api_key",
		Brightness:      50,
		DefaultInterval: 10,
	}
	assert.NoError(t, s.DB.Create(&device).Error, "Failed to create test device")

	// Create a dummy pushed app so we get a real image
	appIname := "ws_test_app"
	app := data.App{
		DeviceID:    deviceID,
		Iname:       appIname,
		Name:        "WS Test App",
		Pushed:      true,
		Enabled:     true,
		Order:       0,
		DisplayTime: 5,
	}
	assert.NoError(t, s.DB.Create(&app).Error, "Failed to create test app")

	// Create dummy WebP file
	webpDir := filepath.Join(s.DataDir, "webp", deviceID, "pushed")
	assert.NoError(t, os.MkdirAll(webpDir, 0755), "Failed to create webp dir")
	// 1x1 green pixel webp
	dummyWebp := []byte{
		0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x20,
		0x18, 0x00, 0x00, 0x00, 0x30, 0x01, 0x00, 0x9d, 0x01, 0x2a, 0x01, 0x00, 0x01, 0x00, 0x00, 0x3e,
		0x0d, 0x03, 0x00, 0xfe, 0xff, 0xff, 0x00, 0x00, 0x00, 0x00,
	}

	webpPath := filepath.Join(webpDir, appIname+".webp")
	assert.NoError(t, os.WriteFile(webpPath, dummyWebp, 0644), "Failed to write dummy webp")

	// Start Test Server
	ts := httptest.NewServer(s)
	defer ts.Close()

	// Construct WS URL
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/" + deviceID + "/ws"

	// Connect WS Client
	dialer := websocket.Dialer{}
	conn, resp, err := dialer.Dial(wsURL, nil)
	assert.NoError(t, err, "WS connection failed")
	defer func() {
		if err := conn.Close(); err != nil {
			t.Logf("Failed to close connection: %v", err)
		}
	}()

	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode, "Expected status 101")

	// Message Expectation Loop
	// We expect:
	// 1. JSON: {"dwell_secs": ...}
	// 2. JSON: {"brightness": ...} (Optional, but likely sent on first connect)
	// 3. Binary: Image Data

	gotDwell := false
	gotBrightness := false
	gotImage := false

	// Set read deadline
	assert.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)), "Failed to set read deadline")

	for range 3 {
		msgType, msgData, err := conn.ReadMessage()
		assert.NoError(t, err, "ReadMessage failed")

		switch msgType {
		case websocket.TextMessage:
			var msgMap map[string]any
			assert.NoError(t, json.Unmarshal(msgData, &msgMap), "Failed to parse JSON", string(msgData))

			if _, ok := msgMap["dwell_secs"]; ok {
				gotDwell = true
			}
			if _, ok := msgMap["brightness"]; ok {
				gotBrightness = true
			}
		case websocket.BinaryMessage:
			gotImage = true
			assert.Equal(t, len(dummyWebp), len(msgData), "Image size mismatch")
		}
	}

	assert.True(t, gotDwell, "Did not receive dwell_secs message")
	// Brightness is only sent if changed from -1, which it is on first connect
	assert.True(t, gotBrightness, "Did not receive brightness message")
	assert.True(t, gotImage, "Did not receive image message")

	// Send Client Info (mimic firmware)
	clientInfo := ClientInfo{
		FirmwareVersion: "1.0.0",
		MACAddress:      "00:11:22:33:44:55",
	}
	msg := WSMessage{
		ClientInfo: &clientInfo,
	}
	assert.NoError(t, conn.WriteJSON(msg), "Failed to write client info")

	// Verify device info updated in DB
	assert.Eventually(t, func() bool {
		var updatedDevice data.Device
		s.DB.First(&updatedDevice, "id = ?", deviceID)
		return updatedDevice.Info.FirmwareVersion == "1.0.0" && updatedDevice.Info.MACAddress == "00:11:22:33:44:55"
	}, 2*time.Second, 100*time.Millisecond, "Device info was not updated in the database in time")

	// Send ACK to simulate display start
	displaying := 1
	ackMsg := WSMessage{
		Displaying: &displaying,
	}
	assert.NoError(t, conn.WriteJSON(ackMsg), "Failed to write ACK")
}
