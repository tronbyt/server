package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tronbyt-server/internal/chunker"
	"tronbyt-server/internal/data"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Dummy 1x1 green webp bytes for testing.
var dummyChunkWebP1 = []byte{
	0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x20,
	0x18, 0x00, 0x00, 0x00, 0x30, 0x01, 0x00, 0x9d, 0x01, 0x2a, 0x01, 0x00, 0x01, 0x00, 0x00, 0x3e,
	0x0d, 0x03, 0x00, 0xfe, 0xff, 0xff, 0x00, 0x00, 0x00, 0x01,
}

var dummyChunkWebP2 = []byte{
	0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x20,
	0x18, 0x00, 0x00, 0x00, 0x30, 0x01, 0x00, 0x9d, 0x01, 0x2a, 0x01, 0x00, 0x01, 0x00, 0x00, 0x3e,
	0x0d, 0x03, 0x00, 0xfe, 0xff, 0xff, 0x00, 0x00, 0x00, 0x02,
}

var dummyChunkWebP3 = []byte{
	0x52, 0x49, 0x46, 0x46, 0x24, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x20,
	0x18, 0x00, 0x00, 0x00, 0x30, 0x01, 0x00, 0x9d, 0x01, 0x2a, 0x01, 0x00, 0x01, 0x00, 0x00, 0x3e,
	0x0d, 0x03, 0x00, 0xfe, 0xff, 0xff, 0x00, 0x00, 0x00, 0x03,
}

func TestDeviceMaxChunkSize_ESP_vs_RPi(t *testing.T) {
	s := newTestServerAPI(t)

	espDevice := &data.Device{
		ID:   "esp1",
		Type: data.DeviceTidbytGen1,
	}
	assert.Equal(t, 450000, s.GetMaxChunkSizeForDevice(espDevice))
	assert.Equal(t, 0, s.GetMaxChunkDurationForDevice(espDevice))

	// Config override for ESP devices
	s.Config.MaxChunkSizeESP = 200000
	assert.Equal(t, 200000, s.GetMaxChunkSizeForDevice(espDevice))
	s.Config.MaxChunkSizeESP = 0 // reset

	// MatrixPortal S3 and Wide devices have 450,000-byte firmware buffers
	matrixDevice := &data.Device{
		ID:   "matrix1",
		Type: data.DeviceMatrixPortal,
	}
	assert.Equal(t, 450000, s.GetMaxChunkSizeForDevice(matrixDevice))
	assert.Equal(t, 0, s.GetMaxChunkDurationForDevice(matrixDevice))

	wideDevice := &data.Device{
		ID:   "wide1",
		Type: data.DeviceTronbytS3Wide,
	}
	assert.Equal(t, 450000, s.GetMaxChunkSizeForDevice(wideDevice))

	matrixWideDevice := &data.Device{
		ID:   "matrixwide1",
		Type: data.DeviceMatrixPortalWide,
	}
	assert.Equal(t, 450000, s.GetMaxChunkSizeForDevice(matrixWideDevice))

	matrixWSDevice := &data.Device{
		ID:   "matrixws1",
		Type: data.DeviceMatrixPortalWS,
	}
	assert.Equal(t, 450000, s.GetMaxChunkSizeForDevice(matrixWSDevice))

	// Config override for ESP devices affects all ESP variants (Gen 1, Gen 2, S3, MatrixPortal)
	s.Config.MaxChunkSizeESP = 200000
	assert.Equal(t, 200000, s.GetMaxChunkSizeForDevice(wideDevice))
	assert.Equal(t, 200000, s.GetMaxChunkSizeForDevice(matrixWideDevice))
	s.Config.MaxChunkSizeESP = 0 // reset

	// Pixoticker has 50,000-byte buffer in firmware
	pixoDevice := &data.Device{
		ID:   "pixo1",
		Type: data.DevicePixoticker,
	}
	assert.Equal(t, 50000, s.GetMaxChunkSizeForDevice(pixoDevice))

	rpiDevice := &data.Device{
		ID:   "rpi1",
		Type: data.DeviceRaspberryPi,
	}
	assert.Equal(t, 5242880, s.GetMaxChunkSizeForDevice(rpiDevice))
	assert.Equal(t, 0, s.GetMaxChunkDurationForDevice(rpiDevice))

	tronberryDevice := &data.Device{
		ID:   "tronberry1",
		Type: data.DeviceTronbytS3,
		Info: data.DeviceInfo{
			FirmwareType: "Tronberry",
		},
	}
	assert.Equal(t, 5242880, s.GetMaxChunkSizeForDevice(tronberryDevice))
	assert.Equal(t, 0, s.GetMaxChunkDurationForDevice(tronberryDevice))
}

func TestMultiChunk_HTTP_SequentialServing(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "chunkuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	device := data.Device{
		ID:              "chunk_dev_1",
		Username:        user.Username,
		Name:            "MultiChunk Dev",
		APIKey:          "key123",
		Brightness:      80,
		DefaultInterval: 10,
		LastAppIndex:    -1,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path1 := "apps/test/app1.webp"
	app1 := data.App{
		DeviceID: device.ID,
		Iname:    "mc1",
		Name:     "Multi-Chunk App",
		Enabled:  true,
		Order:    0,
		Path:     &path1,
		Pushed:   true,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app1))

	path2 := "apps/test/app2.webp"
	app2 := data.App{
		DeviceID:    device.ID,
		Iname:       "single1",
		Name:        "Single-Image App",
		Enabled:     true,
		Order:       1,
		Path:        &path2,
		Pushed:      true,
		DisplayTime: 7,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app2))

	// Write App 1 and App 2 dummy webp
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	app1Path := s.getAppWebpPath(webpDir, &app1)
	require.NoError(t, os.MkdirAll(filepath.Dir(app1Path), 0755))
	require.NoError(t, os.WriteFile(app1Path, dummyChunkWebP1, 0644))

	app2Path := s.getAppWebpPath(webpDir, &app2)
	require.NoError(t, os.MkdirAll(filepath.Dir(app2Path), 0755))
	require.NoError(t, os.WriteFile(app2Path, dummyChunkWebP3, 0644))

	// Save 3 chunks for App 1
	chunks := []chunker.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 3300},
		{WebPBytes: dummyChunkWebP2, DurationMs: 3300},
		{WebPBytes: dummyChunkWebP3, DurationMs: 3400},
	}
	require.NoError(t, s.saveAppChunks(device.ID, &app1, chunks))

	ts := httptest.NewServer(s)
	defer ts.Close()

	client := ts.Client()

	reqNext := func() (int, []byte, string) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/"+device.ID+"/next", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer key123")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		dwell := resp.Header.Get("Tronbyt-Dwell-Secs")
		body := make([]byte, 1024)
		n, _ := resp.Body.Read(body)
		return resp.StatusCode, body[:n], dwell
	}

	// Call 1 -> Expect chunk 0 of App 1 (dwell 3s)
	code, body, dwell := reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, dummyChunkWebP1, body)
	assert.Equal(t, "3", dwell)

	// Call 2 -> Expect chunk 1 of App 1 (dwell 3s)
	code, body, dwell = reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, dummyChunkWebP2, body)
	assert.Equal(t, "3", dwell)

	// Call 3 -> Expect chunk 2 of App 1 (dwell 3s)
	code, body, dwell = reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, dummyChunkWebP3, body)
	assert.Equal(t, "3", dwell)

	// Call 4 -> Sequence finished! Should rotate to App 2
	code, body, dwell = reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, dummyChunkWebP3, body)
	assert.Equal(t, "7", dwell) // App 2 DisplayTime = 7

	// Call 5 -> Should rotate back to App 1, chunk 0
	code, body, dwell = reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, dummyChunkWebP1, body)
	assert.Equal(t, "3", dwell)
}

func TestMultiChunk_HTTP_EphemeralInterrupt(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "chunkuser2"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	device := data.Device{
		ID:              "chunk_dev_2",
		Username:        user.Username,
		Name:            "MultiChunk Dev 2",
		APIKey:          "key456",
		Brightness:      80,
		DefaultInterval: 10,
		LastAppIndex:    -1,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path1 := "apps/test/app1.webp"
	app1 := data.App{
		DeviceID: device.ID,
		Iname:    "mc2",
		Name:     "Multi-Chunk App 2",
		Enabled:  true,
		Order:    0,
		Path:     &path1,
		Pushed:   true,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app1))

	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	app1Path := s.getAppWebpPath(webpDir, &app1)
	require.NoError(t, os.MkdirAll(filepath.Dir(app1Path), 0755))
	require.NoError(t, os.WriteFile(app1Path, dummyChunkWebP1, 0644))

	chunks := []chunker.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 5000},
		{WebPBytes: dummyChunkWebP2, DurationMs: 5000},
	}
	require.NoError(t, s.saveAppChunks(device.ID, &app1, chunks))

	ts := httptest.NewServer(s)
	defer ts.Close()
	client := ts.Client()

	reqNext := func() (int, []byte) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/"+device.ID+"/next", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer key456")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body := make([]byte, 1024)
		n, _ := resp.Body.Read(body)
		return resp.StatusCode, body[:n]
	}

	// 1. Fetch chunk 0
	code, body := reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, dummyChunkWebP1, body)

	// 2. Now push an ephemeral image
	ephemeralBytes := []byte("pushed_notification_image")
	require.NoError(t, s.savePushedImage(device.ID, "", "", ephemeralBytes))

	// 3. Next call must immediately serve the pushed ephemeral image and abort chunk sequence
	code, body = reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, ephemeralBytes, body)

	// 4. Next call should restart App 1 from Chunk 0
	code, body = reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, dummyChunkWebP1, body)
}

func TestMultiChunk_WebSocket_Streaming(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "ws_user"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	device := data.Device{
		ID:              "ws_dev_1",
		Username:        user.Username,
		Name:            "WS MultiChunk Dev",
		APIKey:          "wskey1",
		Brightness:      100,
		DefaultInterval: 10,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path1 := "apps/test/app.webp"
	app1 := data.App{
		DeviceID: device.ID,
		Iname:    "mc_ws",
		Name:     "WS App",
		Enabled:  true,
		Order:    0,
		Path:     &path1,
		Pushed:   true,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app1))

	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	app1Path := s.getAppWebpPath(webpDir, &app1)
	require.NoError(t, os.MkdirAll(filepath.Dir(app1Path), 0755))
	require.NoError(t, os.WriteFile(app1Path, dummyChunkWebP1, 0644))

	chunks := []chunker.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 100},
		{WebPBytes: dummyChunkWebP2, DurationMs: 100},
	}
	require.NoError(t, s.saveAppChunks(device.ID, &app1, chunks))

	ts := httptest.NewServer(s)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/" + device.ID + "/ws"
	header := http.Header{}
	header.Set("Authorization", "Bearer wskey1")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	// Helper to read JSON message from server
	readJSON := func() map[string]any {
		var m map[string]any
		err := conn.ReadJSON(&m)
		require.NoError(t, err)
		return m
	}

	// Helper to read binary message from server
	readBinary := func() []byte {
		mType, data, err := conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, websocket.BinaryMessage, mType)
		return data
	}

	// 1. Chunk 0 metadata: dwell_secs
	meta0 := readJSON()
	assert.Equal(t, float64(1), meta0["dwell_secs"])

	// 2. Brightness message
	brMsg := readJSON()
	assert.Equal(t, float64(100), brMsg["brightness"])

	// 3. Chunk 0 binary data
	bin0 := readBinary()
	assert.Equal(t, dummyChunkWebP1, bin0)

	// Simulate ACK from device for chunk 0
	require.NoError(t, conn.WriteJSON(map[string]any{"displaying": 1}))

	// 4. Chunk 1 metadata
	meta1 := readJSON()
	assert.Equal(t, float64(1), meta1["dwell_secs"])

	// 5. Chunk 1 binary data
	bin1 := readBinary()
	assert.Equal(t, dummyChunkWebP2, bin1)

	// Simulate ACK from device for chunk 1
	require.NoError(t, conn.WriteJSON(map[string]any{"displaying": 2}))
}

func TestMultiChunk_WebSocket_Streaming_DoubleBuffer(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "ws_user2"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	protoVer := 1
	device := data.Device{
		ID:              "ws_dev_db",
		Username:        user.Username,
		Name:            "WS Double Buffer Dev",
		APIKey:          "wskey2",
		Brightness:      100,
		DefaultInterval: 10,
		Info: data.DeviceInfo{
			ProtocolVersion: &protoVer,
		},
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path1 := "apps/test/app.webp"
	app1 := data.App{
		DeviceID: device.ID,
		Iname:    "mc_ws_db",
		Name:     "WS App DB",
		Enabled:  true,
		Order:    0,
		Path:     &path1,
		Pushed:   true,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app1))

	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	app1Path := s.getAppWebpPath(webpDir, &app1)
	require.NoError(t, os.MkdirAll(filepath.Dir(app1Path), 0755))
	require.NoError(t, os.WriteFile(app1Path, dummyChunkWebP1, 0644))

	chunks := []chunker.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 5000},
		{WebPBytes: dummyChunkWebP2, DurationMs: 5000},
	}
	require.NoError(t, s.saveAppChunks(device.ID, &app1, chunks))

	ts := httptest.NewServer(s)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/" + device.ID + "/ws"
	header := http.Header{}
	header.Set("Authorization", "Bearer wskey2")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	readJSON := func() map[string]any {
		var m map[string]any
		err := conn.ReadJSON(&m)
		require.NoError(t, err)
		return m
	}

	readBinary := func() []byte {
		mType, data, err := conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, websocket.BinaryMessage, mType)
		return data
	}

	// 1. Server sends chunk 0 metadata & binary
	meta0 := readJSON()
	assert.Equal(t, float64(5), meta0["dwell_secs"])
	_ = readJSON() // brightness
	bin0 := readBinary()
	assert.Equal(t, dummyChunkWebP1, bin0)

	// Device starts displaying chunk 0 and immediately ACKs
	require.NoError(t, conn.WriteJSON(map[string]any{"displaying": 1}))

	// 2. Under protocol version 1 (double buffer), server should IMMEDIATELY send chunk 1
	// without waiting for the 5-second dwell of chunk 0 to expire!
	meta1 := readJSON()
	assert.Equal(t, float64(5), meta1["dwell_secs"])
	bin1 := readBinary()
	assert.Equal(t, dummyChunkWebP2, bin1)
}

func TestMultiChunk_SaveAppChunksSeparatesFullPreview(t *testing.T) {
	s := newTestServerAPI(t)

	device := data.Device{ID: "save_test_dev"}
	app := data.App{
		DeviceID: device.ID,
		Iname:    "save_app",
		Name:     "SaveApp",
	}

	fullBytes := []byte("full_15s_animation_bytes")
	chunks := []chunker.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 5000, FullWebPBytes: fullBytes},
		{WebPBytes: dummyChunkWebP2, DurationMs: 5000},
	}

	require.NoError(t, s.saveAppChunks(device.ID, &app, chunks))

	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)

	// 1. Primary path should contain chunk 0
	primaryPath := filepath.Join(webpDir, "SaveApp-save_app.webp")
	primaryData, err := os.ReadFile(primaryPath)
	require.NoError(t, err)
	assert.Equal(t, dummyChunkWebP1, primaryData, "Primary file must be chunk 0 for device safety")

	// 2. Full path should contain fullBytes
	fullPath := filepath.Join(webpDir, "SaveApp-save_app_full.webp")
	savedFull, err := os.ReadFile(fullPath)
	require.NoError(t, err)
	assert.Equal(t, fullBytes, savedFull, "_full.webp must contain complete animation")

	// 3. Chunk files
	c0Path := filepath.Join(webpDir, "SaveApp-save_app_chunk_0.webp")
	c1Path := filepath.Join(webpDir, "SaveApp-save_app_chunk_1.webp")
	assert.FileExists(t, c0Path)
	assert.FileExists(t, c1Path)

	// 4. Chunks metadata
	metaPath := filepath.Join(webpDir, "SaveApp-save_app_chunks.json")
	assert.FileExists(t, metaPath)
}

func TestMultiChunk_OversizedPrimaryProtection(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "oversized_user"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	// Gen 1 device with 450KB limit
	device := data.Device{
		ID:       "gen1_oversized",
		Username: user.Username,
		Type:     data.DeviceTidbytGen1,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path := "apps/test/oversized.webp"
	app := data.App{
		DeviceID: device.ID,
		Iname:    "over_app",
		Name:     "OverApp",
		Path:     &path,
		Pushed:   true,
		Enabled:  true,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))
	device.Apps = []*data.App{&app}

	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)

	// Write an oversized primary webp file (> 450,000 bytes)
	primaryPath := filepath.Join(webpDir, "OverApp-over_app.webp")
	oversizedBytes := make([]byte, 500000)
	copy(oversizedBytes, dummyChunkWebP1)
	require.NoError(t, os.WriteFile(primaryPath, oversizedBytes, 0644))

	// Case 1: Oversized primary and NO chunk 0 available -> returns default image
	img, _, err := s.GetNextAppImage(ctx, &device, &user, "")
	require.NoError(t, err)
	assert.NotEqual(t, oversizedBytes, img, "Must not serve oversized primary image")

	// Case 2: Multi-chunk saved with safe chunk 0 -> falls back to chunk 0
	chunks := []chunker.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 5000},
		{WebPBytes: dummyChunkWebP2, DurationMs: 5000},
	}
	require.NoError(t, s.saveAppChunks(device.ID, &app, chunks))
	// Re-overwrite primary with oversized to simulate older primary
	require.NoError(t, os.WriteFile(primaryPath, oversizedBytes, 0644))

	img, _, err = s.GetNextAppImage(ctx, &device, &user, "")
	require.NoError(t, err)
	assert.Equal(t, dummyChunkWebP1, img, "Should safely fall back to chunk 0 when primary is oversized")
}

func TestMultiChunk_WebSocketPreviewPlayback(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	deviceID := "ws_preview_dev"
	user := data.User{Username: "ws_prev_user"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	protoVer := 1
	device := data.Device{
		ID:         deviceID,
		Username:   user.Username,
		APIKey:     "prev_key",
		Brightness: 90,
		Info: data.DeviceInfo{
			ProtocolVersion: &protoVer,
		},
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path := "apps/test/normal.webp"
	app := data.App{
		DeviceID: device.ID,
		Iname:    "normal_app",
		Name:     "NormalApp",
		Path:     &path,
		Pushed:   true,
		Enabled:  true,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))
	device.Apps = []*data.App{&app}

	// Prepare dummy webp for normal app
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	normalPath := filepath.Join(webpDir, "NormalApp-normal_app.webp")
	require.NoError(t, os.WriteFile(normalPath, dummyChunkWebP3, 0644))

	ts := httptest.NewServer(s)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/" + device.ID + "/ws"
	header := http.Header{}
	header.Set("Authorization", "Bearer prev_key")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	readJSON := func() map[string]any {
		var m map[string]any
		err := conn.ReadJSON(&m)
		require.NoError(t, err)
		return m
	}
	readBinary := func() []byte {
		mType, data, err := conn.ReadMessage()
		require.NoError(t, err)
		require.Equal(t, websocket.BinaryMessage, mType)
		return data
	}

	// 1. Initial normal image arrives
	_ = readJSON()   // dwell_secs
	_ = readJSON()   // brightness
	_ = readBinary() // normal image
	require.NoError(t, conn.WriteJSON(map[string]any{"displaying": 1}))

	// 2. Broadcast a DevicePreviewMessage with 2 preview chunks
	previewApp := data.App{
		DeviceID: device.ID,
		Iname:    "preview_app",
		Name:     "PreviewApp",
	}
	previewChunks := []chunker.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 4000},
		{WebPBytes: dummyChunkWebP2, DurationMs: 4000},
	}

	s.Broadcaster.Notify(device.ID, DevicePreviewMessage{
		App:    &previewApp,
		Chunks: previewChunks,
	})

	// 3. Device should receive chunk 0 of preview with immediate: true
	meta0 := readJSON()
	assert.Equal(t, float64(4), meta0["dwell_secs"])
	bin0 := readBinary()
	assert.Equal(t, dummyChunkWebP1, bin0)
	immMsg := readJSON()
	assert.Equal(t, true, immMsg["immediate"])

	// ACK chunk 0
	require.NoError(t, conn.WriteJSON(map[string]any{"displaying": 2}))

	// 4. Device should receive chunk 1 of preview
	meta1 := readJSON()
	assert.Equal(t, float64(4), meta1["dwell_secs"])
	bin1 := readBinary()
	assert.Equal(t, dummyChunkWebP2, bin1)
}

func TestStarlark_AutoChunking_OversizedAnimation(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "starlark_chunk_user"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	// Force 2KB chunk budget to test auto-partitioning on 3.5KB test animation
	s.Config.MaxChunkSizeESP = 2000

	device := data.Device{
		ID:              "starlark_chunk_dev",
		Username:        user.Username,
		Name:            "Starlark Chunk Device",
		Type:            data.DeviceTidbytGen1,
		DefaultInterval: 10,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	// Create 40 frames of high-entropy noise animation (> 3,000 bytes WebP)
	frames := make([]*image.RGBA, 40)
	for i := range 40 {
		img := image.NewRGBA(image.Rect(0, 0, 64, 32))
		for y := range 32 {
			for x := range 64 {
				img.SetRGBA(x, y, color.RGBA{
					R: uint8((x*3 + i*11) % 256),
					G: uint8((y*5 + i*13) % 256),
					B: uint8((x + y + i*17) % 256),
					A: 255,
				})
			}
		}
		frames[i] = img
	}

	rawWebP, err := chunker.EncodeFramesToWebP(frames, 50*time.Millisecond)
	require.NoError(t, err)
	rawSize := len(rawWebP)
	require.Greater(t, rawSize, 2000, "Noise animation must exceed 2000 bytes")

	// Set up an app pointing to a static webp that exceeds the device budget
	appDir := filepath.Join(s.DataDir, "apps", "test_oversized_anim")
	require.NoError(t, os.MkdirAll(appDir, 0755))
	animPath := filepath.Join(appDir, "anim.webp")
	require.NoError(t, os.WriteFile(animPath, rawWebP, 0644))

	relPath := filepath.Join("apps", "test_oversized_anim", "anim.webp")
	app := data.App{
		DeviceID:   device.ID,
		Iname:      "oversized_iname",
		Name:       "Oversized Animation",
		Enabled:    true,
		Path:       &relPath,
		UInterval:  1,
		LastRender: time.Now().Add(-10 * time.Minute),
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))

	// Render the app using possiblyRender
	rendered := s.possiblyRender(ctx, &app, &device, &user)
	require.True(t, rendered)

	// Verify that chunks were created because the animation exceeded maxChunkSize
	chunks := s.getAppChunks(device.ID, &app)
	require.NotEmpty(t, chunks)
	maxLimit := s.GetMaxChunkSizeForDevice(&device)
	for idx, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk.WebPBytes), maxLimit, "Chunk %d size (%d) must not exceed maxLimit (%d)", idx, len(chunk.WebPBytes), maxLimit)
	}

	// Check that chunk files exist on disk
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	chunk0Path := filepath.Join(webpDir, "Oversized Animation-oversized_iname_chunk_0.webp")
	assert.FileExists(t, chunk0Path)

	// Check that full WebP exists on disk for browser preview
	fullPath := filepath.Join(webpDir, "Oversized Animation-oversized_iname_full.webp")
	assert.FileExists(t, fullPath)
	fullBytes, err := os.ReadFile(fullPath)
	require.NoError(t, err)
	assert.NotEmpty(t, fullBytes)
	decodedFull, _, err := chunker.DecodeWebPAnimation(fullBytes)
	require.NoError(t, err)
	assert.Len(t, decodedFull, 40)
}

func TestStarlark_AutoChunking_PushPreview(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "preview_chunk_user"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	// Force 2KB chunk budget to test preview partitioning on 3.5KB test animation
	s.Config.MaxChunkSizeESP = 2000

	device := data.Device{
		ID:              "preview_chunk_dev",
		Username:        user.Username,
		Name:            "Preview Chunk Device",
		Type:            data.DeviceTidbytGen1,
		DefaultInterval: 10,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	// Create a test animated webp file for a static webp/starlark app
	frames := make([]*image.RGBA, 40)
	for i := range 40 {
		img := image.NewRGBA(image.Rect(0, 0, 64, 32))
		for y := range 32 {
			for x := range 64 {
				img.SetRGBA(x, y, color.RGBA{
					R: uint8((x*5 + i*13) % 256),
					G: uint8((y*7 + i*17) % 256),
					B: uint8((x + y + i*23) % 256),
					A: 255,
				})
			}
		}
		frames[i] = img
	}

	rawWebP, err := chunker.EncodeFramesToWebP(frames, 50*time.Millisecond)
	require.NoError(t, err)

	appDir := filepath.Join(s.DataDir, "apps", "test_preview_anim")
	require.NoError(t, os.MkdirAll(appDir, 0755))
	animPath := filepath.Join(appDir, "anim.webp")
	require.NoError(t, os.WriteFile(animPath, rawWebP, 0644))

	relPath := filepath.Join("apps", "test_preview_anim", "anim.webp")
	app := data.App{
		DeviceID:   device.ID,
		Iname:      "preview_anim_iname",
		Name:       "Push Preview Animation",
		Enabled:    true,
		Path:       &relPath,
		UInterval:  1,
		LastRender: time.Now().Add(-10 * time.Minute),
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))

	ch := s.Broadcaster.Subscribe(device.ID)
	defer s.Broadcaster.Unsubscribe(device.ID, ch)

	// Call handlePushPreview
	req, _ := http.NewRequest(http.MethodPost, "/devices/"+device.ID+"/"+app.Iname+"/preview", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	reqCtx := context.WithValue(req.Context(), userContextKey, &user)
	reqCtx = context.WithValue(reqCtx, deviceContextKey, &device)
	reqCtx = context.WithValue(reqCtx, appContextKey, &app)
	req = req.WithContext(reqCtx)

	rr := httptest.NewRecorder()
	s.handlePushPreview(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code, "Response: %s", rr.Body.String())

	// 1. Broadcaster must receive DevicePreviewMessage with chunks
	select {
	case msg := <-ch:
		previewMsg, ok := msg.(DevicePreviewMessage)
		require.True(t, ok, "Broadcaster must receive DevicePreviewMessage for oversized Starlark animation")
		assert.Equal(t, app.Iname, previewMsg.App.Iname)
		require.GreaterOrEqual(t, len(previewMsg.Chunks), 2, "Must be partitioned into at least 2 chunks")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for preview message on Broadcaster")
	}

	// 2. Ephemeral push image was saved for the device and fits within buffer budget
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	pushedPath := filepath.Join(webpDir, "pushed", app.Iname+".webp")
	assert.FileExists(t, pushedPath)
	pushedBytes, err := os.ReadFile(pushedPath)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(pushedBytes), 2000, "Ephemeral push preview must fit within device chunk size limit")

	// 3. Draft preview must NOT overwrite installed app's persistent chunks on disk
	appChunksPath := filepath.Join(webpDir, fmt.Sprintf("%s-%s_chunks.json", app.Name, app.Iname))
	assert.NoFileExists(t, appChunksPath, "Preview render must not overwrite persistent app chunks")
}

func TestPushImage_AutoChunking_Persistent(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "push_chunk_user"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	// Cap device chunk budget at 2000 bytes
	s.Config.MaxChunkSizeESP = 2000

	device := data.Device{
		ID:              "push_chunk_dev",
		Username:        user.Username,
		Name:            "Push Chunk Device",
		Type:            data.DeviceTidbytGen1,
		APIKey:          "push_chunk_api_key",
		DefaultInterval: 10,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	// Create 40 frames of high-entropy noise animation (> 3,000 bytes WebP)
	frames := make([]*image.RGBA, 40)
	for i := range 40 {
		img := image.NewRGBA(image.Rect(0, 0, 64, 32))
		for y := range 32 {
			for x := range 64 {
				img.SetRGBA(x, y, color.RGBA{
					R: uint8((x*3 + i*11) % 256),
					G: uint8((y*5 + i*13) % 256),
					B: uint8((x + y + i*17) % 256),
					A: 255,
				})
			}
		}
		frames[i] = img
	}

	rawWebP, err := chunker.EncodeFramesToWebP(frames, 50*time.Millisecond)
	require.NoError(t, err)
	require.Greater(t, len(rawWebP), 2000, "Raw animation must exceed 2000 bytes")

	ch := s.Broadcaster.Subscribe(device.ID)
	defer s.Broadcaster.Unsubscribe(device.ID, ch)

	installID := "pushed_noise_app"
	pushPayload := PushData{
		InstallationID: installID,
		Image:          base64.StdEncoding.EncodeToString(rawWebP),
	}
	body, err := json.Marshal(pushPayload)
	require.NoError(t, err)

	req := newAPIRequest(http.MethodPost, "/v0/devices/"+device.ID+"/push", device.APIKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code, "Response: %s", rr.Body.String())

	// 1. Broadcaster must receive DevicePreviewMessage with partitioned chunks
	select {
	case msg := <-ch:
		previewMsg, ok := msg.(DevicePreviewMessage)
		require.True(t, ok, "Broadcaster must receive DevicePreviewMessage for oversized push")
		assert.NotNil(t, previewMsg.App)
		require.GreaterOrEqual(t, len(previewMsg.Chunks), 2, "Must be partitioned into at least 2 chunks")
		maxLimit := s.GetMaxChunkSizeForDevice(&device)
		for idx, chunk := range previewMsg.Chunks {
			assert.LessOrEqual(t, len(chunk.WebPBytes), maxLimit, "Chunk %d exceeds limit", idx)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for preview message on Broadcaster")
	}

	// 2. Persistent app record created in DB
	app, err := gorm.G[data.App](s.DB).Where("device_id = ? AND path = ?", device.ID, "pushed:"+installID).First(ctx)
	require.NoError(t, err)
	assert.True(t, app.Pushed)

	// 3. Chunks on disk and in getAppChunks
	chunks := s.getAppChunks(device.ID, &app)
	require.GreaterOrEqual(t, len(chunks), 2)

	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	primaryPath := filepath.Join(webpDir, "pushed", installID+".webp")
	assert.FileExists(t, primaryPath)
	primaryBytes, err := os.ReadFile(primaryPath)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(primaryBytes), 2000, "Primary WebP must be safe Chunk 0 size")

	// 4. HTTP sequential chunk serving
	reloadedDevice, err := s.reloadDevice(device.ID)
	require.NoError(t, err)
	device = *reloadedDevice

	// First poll returns chunk 0
	c0Bytes, servedApp, dwell0, err := s.GetNextAppImageHTTP(ctx, &device, &user, "")
	require.NoError(t, err)
	require.NotNil(t, servedApp)
	assert.Equal(t, app.Iname, servedApp.Iname)
	assert.Equal(t, chunks[0].WebPBytes, c0Bytes)
	assert.Greater(t, dwell0, 0)

	// Second poll returns chunk 1
	c1Bytes, servedApp1, dwell1, err := s.GetNextAppImageHTTP(ctx, &device, &user, "")
	require.NoError(t, err)
	require.NotNil(t, servedApp1)
	assert.Equal(t, app.Iname, servedApp1.Iname)
	assert.Equal(t, chunks[1].WebPBytes, c1Bytes)
	assert.Greater(t, dwell1, 0)

	// 5. Delete installation via API cleans up chunk files and invalidates cache
	delReq := newAPIRequest(http.MethodDelete, "/v0/devices/"+device.ID+"/installations/"+installID, device.APIKey, nil)
	delRr := httptest.NewRecorder()
	s.ServeHTTP(delRr, delReq)
	assert.Equal(t, http.StatusOK, delRr.Code)

	// Chunks should be cleaned up
	assert.Empty(t, s.getAppChunks(device.ID, &app))
}

func TestPushImage_AutoChunking_Ephemeral(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "push_ephem_user"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	s.Config.MaxChunkSizeESP = 2000

	device := data.Device{
		ID:              "push_ephem_dev",
		Username:        user.Username,
		Name:            "Push Ephem Device",
		Type:            data.DeviceTidbytGen1,
		APIKey:          "ephem_api_key",
		DefaultInterval: 10,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	// Create 40 frames of high-entropy noise animation (> 3,000 bytes WebP)
	frames := make([]*image.RGBA, 40)
	for i := range 40 {
		img := image.NewRGBA(image.Rect(0, 0, 64, 32))
		for y := range 32 {
			for x := range 64 {
				img.SetRGBA(x, y, color.RGBA{
					R: uint8((x*3 + i*11) % 256),
					G: uint8((y*5 + i*13) % 256),
					B: uint8((x + y + i*17) % 256),
					A: 255,
				})
			}
		}
		frames[i] = img
	}

	rawWebP, err := chunker.EncodeFramesToWebP(frames, 50*time.Millisecond)
	require.NoError(t, err)
	require.Greater(t, len(rawWebP), 2000)

	ch := s.Broadcaster.Subscribe(device.ID)

	// Ephemeral push without installationID
	pushPayload := PushData{
		Image: base64.StdEncoding.EncodeToString(rawWebP),
	}
	body, err := json.Marshal(pushPayload)
	require.NoError(t, err)

	req := newAPIRequest(http.MethodPost, "/v0/devices/"+device.ID+"/push", device.APIKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code, "Response: %s", rr.Body.String())

	// 1. Broadcaster must receive DevicePreviewMessage for streaming
	select {
	case msg := <-ch:
		previewMsg, ok := msg.(DevicePreviewMessage)
		require.True(t, ok, "Broadcaster must receive DevicePreviewMessage for ephemeral push")
		assert.Nil(t, previewMsg.App)
		require.GreaterOrEqual(t, len(previewMsg.Chunks), 2)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for preview message on Broadcaster")
	}

	// Active subscriber consumed it, so no disk fallback needed
	s.Broadcaster.Unsubscribe(device.ID, ch)

	// 2. Now push again when no WebSocket subscriber is listening: fallback image in pushed/ is safely sized Chunk 0
	rr2 := httptest.NewRecorder()
	req2 := newAPIRequest(http.MethodPost, "/v0/devices/"+device.ID+"/push", device.APIKey, body)
	s.ServeHTTP(rr2, req2)
	assert.Equal(t, http.StatusOK, rr2.Code)

	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	pushedDir := filepath.Join(webpDir, "pushed")
	entries, err := os.ReadDir(pushedDir)
	require.NoError(t, err)
	require.NotEmpty(t, entries)
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "__") && strings.HasSuffix(entry.Name(), ".webp") {
			info, err := entry.Info()
			require.NoError(t, err)
			assert.LessOrEqual(t, info.Size(), int64(2000), "Ephemeral image must not exceed chunk limit")
		}
	}
}
