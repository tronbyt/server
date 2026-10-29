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
	"tronbyt-server/internal/providers"

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

type mockExternalProvider struct {
	name       string
	apps       []providers.ExternalAppMeta
	chunks     []providers.RenderChunk
	renderFunc func(ctx context.Context, req providers.RenderRequest) ([]providers.RenderChunk, error)
}

func (m *mockExternalProvider) Name() string { return m.name }
func (m *mockExternalProvider) ListApps(ctx context.Context) ([]providers.ExternalAppMeta, error) {
	return m.apps, nil
}
func (m *mockExternalProvider) GetAppSchema(ctx context.Context, appID string) ([]byte, error) {
	includeScaleFilter := true
	if v := ctx.Value(providers.Supports2xKey); v != nil {
		if b, ok := v.(bool); ok {
			includeScaleFilter = b
		}
	}
	if includeScaleFilter {
		return []byte(`{"version":"1","schema":[{"id":"param1"},{"id":"scale_filter"}]}`), nil
	}
	return []byte(`{"version":"1","schema":[{"id":"param1"}]}`), nil
}
func (m *mockExternalProvider) RenderAppChunks(ctx context.Context, req providers.RenderRequest) ([]providers.RenderChunk, error) {
	if m.renderFunc != nil {
		return m.renderFunc(ctx, req)
	}
	return m.chunks, nil
}

func TestDeviceMaxChunkSize_ESP_vs_RPi(t *testing.T) {
	s := newTestServerAPI(t)
	s.Config.MaxChunkSizeESP = 450000
	s.Config.MaxChunkSizeRPi = 5242880

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

	// App 1: Multi-chunk app (3 chunks)
	path1 := "screeny:clock"
	app1 := data.App{
		DeviceID: device.ID,
		Iname:    "app1",
		Name:     "Clock",
		Enabled:  true,
		Order:    0,
		Path:     &path1,
		Pushed:   false,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app1))

	// App 2: Standard single-chunk app
	path2 := "pushed:2"
	app2 := data.App{
		DeviceID:    device.ID,
		Iname:       "app2",
		Name:        "Weather",
		Enabled:     true,
		Order:       1,
		Path:        &path2,
		Pushed:      true,
		DisplayTime: 7,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app2))

	// Write App 2 dummy webp
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	app2Path := s.getAppWebpPath(webpDir, &app2)
	require.NoError(t, os.MkdirAll(filepath.Dir(app2Path), 0755))
	require.NoError(t, os.WriteFile(app2Path, dummyChunkWebP3, 0644))

	// Save 3 chunks for App 1
	chunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 3300},
		{WebPBytes: dummyChunkWebP2, DurationMs: 3300},
		{WebPBytes: dummyChunkWebP3, DurationMs: 3400},
	}
	s.RegisterExternalProvider(&mockExternalProvider{name: "screeny", chunks: chunks})
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

	// Call 4 -> All chunks of App 1 done! Rotates to App 2 (dwell 7s)
	code, body, dwell = reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, dummyChunkWebP3, body)
	assert.Equal(t, "7", dwell)

	// Call 5 -> Rotates back to App 1, chunk 0!
	code, body, dwell = reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, dummyChunkWebP1, body)
	assert.Equal(t, "3", dwell)
}

func TestMultiChunk_HTTP_EphemeralInterrupt(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "ephuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	device := data.Device{
		ID:              "chunk_dev_eph",
		Username:        user.Username,
		Name:            "Eph Dev",
		APIKey:          "keyeph",
		Brightness:      80,
		DefaultInterval: 10,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path1 := "screeny:clock"
	app1 := data.App{
		DeviceID: device.ID,
		Iname:    "app1",
		Name:     "Clock",
		Enabled:  true,
		Order:    0,
		Path:     &path1,
		Pushed:   false,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app1))

	chunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 3300},
		{WebPBytes: dummyChunkWebP2, DurationMs: 3300},
	}
	s.RegisterExternalProvider(&mockExternalProvider{name: "screeny", chunks: chunks})
	require.NoError(t, s.saveAppChunks(device.ID, &app1, chunks))

	ts := httptest.NewServer(s)
	defer ts.Close()

	client := ts.Client()
	reqNext := func() (int, []byte) {
		req, err := http.NewRequest(http.MethodGet, ts.URL+"/"+device.ID+"/next", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer keyeph")
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body := make([]byte, 1024)
		n, _ := resp.Body.Read(body)
		return resp.StatusCode, body[:n]
	}

	// Request 1: serves chunk 0
	code, body := reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, dummyChunkWebP1, body)

	// Now push an ephemeral image
	pushedDir := filepath.Join(s.DataDir, "webp", device.ID, "pushed")
	require.NoError(t, os.MkdirAll(pushedDir, 0755))
	ephemeralPath := filepath.Join(pushedDir, "__ephemeral_test.webp")
	require.NoError(t, os.WriteFile(ephemeralPath, dummyChunkWebP3, 0644))

	// Request 2: should immediately serve ephemeral image and interrupt chunk sequence
	code, body = reqNext()
	assert.Equal(t, http.StatusOK, code)
	assert.Equal(t, dummyChunkWebP3, body)
}

func TestMultiChunk_WebSocket_Streaming(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "wsuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	deviceID := "ws_chunk_dev"
	device := data.Device{
		ID:              deviceID,
		Username:        user.Username,
		Name:            "WS Chunk Dev",
		APIKey:          "wskey",
		Brightness:      50,
		DefaultInterval: 10,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path1 := "screeny:anim"
	app1 := data.App{
		DeviceID: deviceID,
		Iname:    "anim1",
		Name:     "Screeny Anim",
		Enabled:  true,
		Order:    0,
		Path:     &path1,
		Pushed:   false,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app1))

	// 2 chunks with short durations for testing (100ms each)
	chunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 100},
		{WebPBytes: dummyChunkWebP2, DurationMs: 100},
	}
	s.RegisterExternalProvider(&mockExternalProvider{name: "screeny", chunks: chunks})
	require.NoError(t, s.saveAppChunks(deviceID, &app1, chunks))

	ts := httptest.NewServer(s)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/" + deviceID + "/ws"
	dialer := websocket.Dialer{}
	conn, resp, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	assert.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	// Helper to read messages until a binary image is received
	readUntilImage := func() []byte {
		for {
			msgType, data, err := conn.ReadMessage()
			require.NoError(t, err)
			if msgType == websocket.BinaryMessage {
				return data
			}
			if msgType == websocket.TextMessage {
				var m map[string]any
				_ = json.Unmarshal(data, &m)
			}
		}
	}

	// 1. Should receive chunk 0 first
	img0 := readUntilImage()
	assert.Equal(t, dummyChunkWebP1, img0)

	// Send an ack from client
	_ = conn.WriteJSON(WSMessage{Displaying: new(int)})

	// 2. Should receive chunk 1 next after ~100ms
	start := time.Now()
	img1 := readUntilImage()
	elapsed := time.Since(start)
	assert.Equal(t, dummyChunkWebP2, img1)
	assert.GreaterOrEqual(t, elapsed.Milliseconds(), int64(70), "Should wait approximately chunk DurationMs before sending next chunk")
}

func TestMultiChunk_WebSocket_Streaming_DoubleBuffer(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "wsdbuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	protoV1 := 1
	deviceID := "ws_db_chunk_dev"
	device := data.Device{
		ID:              deviceID,
		Username:        user.Username,
		Name:            "WS DB Dev",
		APIKey:          "wsdbkey",
		Brightness:      50,
		DefaultInterval: 10,
		Info: data.DeviceInfo{
			ProtocolVersion: &protoV1,
		},
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path1 := "screeny:anim"
	app1 := data.App{
		DeviceID: deviceID,
		Iname:    "anim1",
		Name:     "Screeny Anim",
		Enabled:  true,
		Order:    0,
		Path:     &path1,
		Pushed:   false,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app1))

	chunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 100},
		{WebPBytes: dummyChunkWebP2, DurationMs: 100},
	}
	s.RegisterExternalProvider(&mockExternalProvider{name: "screeny", chunks: chunks})
	require.NoError(t, s.saveAppChunks(deviceID, &app1, chunks))

	ts := httptest.NewServer(s)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/" + deviceID + "/ws"
	dialer := websocket.Dialer{}
	conn, resp, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	assert.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	readUntilImage := func() ([]byte, []map[string]any) {
		var textMessages []map[string]any
		for {
			msgType, data, err := conn.ReadMessage()
			require.NoError(t, err)
			if msgType == websocket.BinaryMessage {
				return data, textMessages
			}
			if msgType == websocket.TextMessage {
				var m map[string]any
				_ = json.Unmarshal(data, &m)
				textMessages = append(textMessages, m)
			}
		}
	}

	// 1. Should receive chunk 0 first
	img0, _ := readUntilImage()
	assert.Equal(t, dummyChunkWebP1, img0)

	// Send displaying ACK from client indicating chunk 0 transitioned to screen
	_ = conn.WriteJSON(WSMessage{Displaying: new(int)})

	// 2. Double-buffering: Server queues chunk 1 immediately into empty queue buffer (sub-50ms)
	start := time.Now()
	img1, texts := readUntilImage()
	elapsed := time.Since(start)
	assert.Equal(t, dummyChunkWebP2, img1)
	assert.Less(t, elapsed.Milliseconds(), int64(80), "Double-buffering should push chunk 1 to device queue immediately after chunk 0 starts displaying")

	// Ensure chunk 1 was queued smoothly without immediate: true
	for _, m := range texts {
		if imm, ok := m["immediate"].(bool); ok && imm {
			t.Errorf("chunk 1 should not have immediate: true in double-buffering mode")
		}
	}
}

func TestConfigApp_ResetsLastRenderAndClearsChunks(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "cfguser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	device := data.Device{
		ID:              "cfg_dev",
		Username:        user.Username,
		Name:            "Config Dev",
		APIKey:          "cfgkey",
		Brightness:      50,
		DefaultInterval: 10,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path1 := "screeny:knot"
	pastRender := time.Now().Add(-5 * time.Minute)
	app := data.App{
		DeviceID:   device.ID,
		Iname:      "knot1",
		Name:       "Knot",
		Enabled:    true,
		Order:      0,
		Path:       &path1,
		Pushed:     false,
		LastRender: pastRender,
		Config:     map[string]any{"speed": 1.0},
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))

	// Pre-seed chunk cache
	initialChunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 5000},
	}
	s.RegisterExternalProvider(&mockExternalProvider{name: "screeny", chunks: initialChunks})
	require.NoError(t, s.saveAppChunks(device.ID, &app, initialChunks))

	// Verify chunk cache has it
	key := s.getAppChunksCacheKey(device.ID, app.Iname)
	s.appChunksMu.RLock()
	assert.NotEmpty(t, s.appChunksCache[key])
	s.appChunksMu.RUnlock()

	// Simulate config update
	ts := httptest.NewServer(s)
	defer ts.Close()

	payload := map[string]any{
		"enabled":   true,
		"uinterval": 15,
		"config":    map[string]any{"speed": 0.2},
	}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, ts.URL+"/devices/"+device.ID+"/"+app.Iname+"/config", strings.NewReader(string(body)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	// Set session cookie for authenticated user
	session, _ := s.Store.Get(req, "session-name")
	session.Values["username"] = user.Username
	rec := httptest.NewRecorder()
	_ = session.Save(req, rec)
	req.Header.Set("Cookie", rec.Header().Get("Set-Cookie"))

	// Update mock provider to return new chunks with new config
	newChunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP2, DurationMs: 5000},
	}
	s.RegisterExternalProvider(&mockExternalProvider{name: "screeny", chunks: newChunks})

	resp, err := ts.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	// Wait for background render to finish
	s.backgroundRenders.Wait()

	// Verify new chunks were rendered and saved
	reloadedChunks := s.getAppChunks(device.ID, &app)
	require.NotEmpty(t, reloadedChunks)
	assert.Equal(t, dummyChunkWebP2, reloadedChunks[0].WebPBytes)

	// Verify DB app config was updated
	updatedApp, err := gorm.G[data.App](s.DB).Where("id = ?", app.ID).First(ctx)
	require.NoError(t, err)
	assert.Equal(t, float64(0.2), updatedApp.Config["speed"])
}

func TestMultiChunk_SaveAppChunksSeparatesFullPreview(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	device := data.Device{
		ID:   "preview_dev",
		Type: data.DeviceTidbytGen1,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	app := data.App{
		DeviceID: device.ID,
		Iname:    "flock_app",
		Name:     "Flock",
	}

	fullBytes := []byte("large_full_preview_bytes_here")
	chunk0Bytes := dummyChunkWebP1
	chunk1Bytes := dummyChunkWebP2

	chunks := []providers.RenderChunk{
		{WebPBytes: chunk0Bytes, DurationMs: 5000, FullWebPBytes: fullBytes},
		{WebPBytes: chunk1Bytes, DurationMs: 5000},
	}

	require.NoError(t, s.saveAppChunks(device.ID, &app, chunks))

	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)

	// Primary file must be chunk 0 bytes, NOT fullBytes
	primaryPath := s.getAppWebpPath(webpDir, &app)
	savedPrimary, err := os.ReadFile(primaryPath)
	require.NoError(t, err)
	assert.Equal(t, chunk0Bytes, savedPrimary, "primary webp must be chunk 0 to stay within device limits")

	// _full.webp must exist and contain fullBytes
	fullPath := filepath.Join(webpDir, "Flock-flock_app_full.webp")
	savedFull, err := os.ReadFile(fullPath)
	require.NoError(t, err)
	assert.Equal(t, fullBytes, savedFull, "_full.webp must store full animation for web UI")
}

func TestMultiChunk_OversizedPrimaryProtection(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "oversize_user"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	// Gen 1 device with 450KB limit
	device := data.Device{
		ID:           "oversize_dev",
		Username:     user.Username,
		Type:         data.DeviceTidbytGen1,
		LastAppIndex: -1,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	now := time.Now()
	path := "screeny:metaballs"
	app := data.App{
		DeviceID:             device.ID,
		Iname:                "meta_app",
		Name:                 "Metaballs",
		Enabled:              true,
		Order:                0,
		Path:                 &path,
		LastRender:           now,
		LastSuccessfulRender: &now,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))
	device.Apps = []*data.App{&app}

	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)

	// Simulate an oversized legacy primary file on disk (500 KB > 450 KB ESP32 limit)
	oversizedBytes := make([]byte, 500000)
	copy(oversizedBytes, dummyChunkWebP1)
	primaryPath := s.getAppWebpPath(webpDir, &app)
	require.NoError(t, os.WriteFile(primaryPath, oversizedBytes, 0644))

	// Multi-chunk files on disk have a valid chunk 0 that fits
	chunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 5000},
		{WebPBytes: dummyChunkWebP2, DurationMs: 5000},
	}
	s.RegisterExternalProvider(&mockExternalProvider{name: "screeny", chunks: chunks})
	key := s.getAppChunksCacheKey(device.ID, app.Iname)
	s.appChunksMu.Lock()
	s.appChunksCache[key] = chunks
	s.appChunksMu.Unlock()

	// Calling GetNextAppImage should catch that primary exceeds 200000 and fall back to chunk 0
	imgData, returnedApp, err := s.GetNextAppImage(ctx, &device, &user, "")
	require.NoError(t, err)
	assert.Equal(t, app.Iname, returnedApp.Iname)
	assert.Equal(t, dummyChunkWebP1, imgData, "should have fallen back to chunk 0 instead of serving 500KB file")
}

func TestMultiChunk_PushPreviewSavesChunksAndBroadcastsPreviewMessage(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "preview_user"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	device := data.Device{
		ID:              "preview_dev",
		Username:        user.Username,
		Type:            data.DeviceTidbytGen1,
		DefaultInterval: 15,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path := "screeny:metaballs"
	app := data.App{
		DeviceID: device.ID,
		Iname:    "meta_preview",
		Name:     "Metaballs",
		Path:     &path,
		Enabled:  true,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))
	device.Apps = []*data.App{&app}

	fullBytes := []byte("full_15s_uninterrupted_metaballs_animation")
	chunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 5000, FullWebPBytes: fullBytes},
		{WebPBytes: dummyChunkWebP2, DurationMs: 5000},
		{WebPBytes: dummyChunkWebP3, DurationMs: 5000},
	}
	s.RegisterExternalProvider(&mockExternalProvider{name: "screeny", chunks: chunks})

	// Subscribe to device broadcaster before pushing preview
	ch := s.Broadcaster.Subscribe(device.ID)
	defer s.Broadcaster.Unsubscribe(device.ID, ch)

	// Call handlePushPreview
	req, _ := http.NewRequest(http.MethodPost, "/devices/"+device.ID+"/"+app.Iname+"/preview", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	ctx = context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	s.handlePushPreview(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)

	// 1. Broadcaster must receive DevicePreviewMessage with all 3 chunks
	select {
	case msg := <-ch:
		previewMsg, ok := msg.(DevicePreviewMessage)
		require.True(t, ok, "Broadcaster must receive DevicePreviewMessage")
		assert.Equal(t, app.Iname, previewMsg.App.Iname)
		require.Len(t, previewMsg.Chunks, 3, "All 3 chunks must be broadcast for uninterrupted preview")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for preview message on Broadcaster")
	}

	// 2. _full.webp must be saved to disk
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	fullPath := filepath.Join(webpDir, "Metaballs-meta_preview_full.webp")
	savedFull, err := os.ReadFile(fullPath)
	require.NoError(t, err)
	assert.Equal(t, fullBytes, savedFull, "_full.webp must store full animation for web UI")

	// 3. Chunk files must be saved
	chunk0Path := filepath.Join(webpDir, "Metaballs-meta_preview_chunk_0.webp")
	assert.FileExists(t, chunk0Path)
	chunk1Path := filepath.Join(webpDir, "Metaballs-meta_preview_chunk_1.webp")
	assert.FileExists(t, chunk1Path)
	chunk2Path := filepath.Join(webpDir, "Metaballs-meta_preview_chunk_2.webp")
	assert.FileExists(t, chunk2Path)

	// 4. deviceCurrentChunkApp must be tracked for HTTP /next playback
	s.deviceChunkMu.Lock()
	assert.Equal(t, app.Iname, s.deviceCurrentChunkApp[device.ID])
	assert.Equal(t, 0, s.deviceChunkIndices[device.ID])
	s.deviceChunkMu.Unlock()
}

func TestMultiChunk_ConfigPreviewServesFullUninterruptedAnimation(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "cfg_user"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	device := data.Device{
		ID:              "cfg_dev",
		Username:        user.Username,
		Type:            data.DeviceTidbytGen1,
		DefaultInterval: 15,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path := "screeny:metaballs"
	app := data.App{
		DeviceID: device.ID,
		Iname:    "meta_cfg",
		Name:     "Metaballs",
		Path:     &path,
		Enabled:  true,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))
	device.Apps = []*data.App{&app}

	fullBytes := []byte("complete_uninterrupted_15s_animation")
	chunks := []providers.RenderChunk{
		{WebPBytes: fullBytes, DurationMs: 15000, FullWebPBytes: fullBytes},
	}
	s.RegisterExternalProvider(&mockExternalProvider{name: "screeny", chunks: chunks})

	// Case 1: _full.webp does NOT exist on disk yet. handleRenderConfigPreview should render on-demand
	// and save _full.webp.
	req, _ := http.NewRequest(http.MethodGet, "/devices/"+device.ID+"/installations/"+app.Iname+"/preview", nil)
	ctx = context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	s.handleRenderConfigPreview(rr, req)
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, fullBytes, rr.Body.Bytes(), "Must serve complete unchunked animation")

	// Check that _full.webp was written to disk
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	fullPath := filepath.Join(webpDir, "Metaballs-meta_cfg_full.webp")
	savedFull, err := os.ReadFile(fullPath)
	require.NoError(t, err)
	assert.Equal(t, fullBytes, savedFull)

	// Case 2: Subsequent request serves directly from _full.webp on disk
	rr2 := httptest.NewRecorder()
	s.handleRenderConfigPreview(rr2, req)
	assert.Equal(t, http.StatusOK, rr2.Code)
	assert.Equal(t, fullBytes, rr2.Body.Bytes(), "Must serve cached _full.webp directly")
}

func TestMultiChunk_WebSocketPreviewPlayback(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	deviceID := "ws_preview_dev"
	user := data.User{Username: "ws_prev_user"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	protoV1 := 1
	device := data.Device{
		ID:              deviceID,
		Username:        user.Username,
		Name:            "WS Preview Dev",
		APIKey:          "wsprevkey",
		Brightness:      50,
		DefaultInterval: 10,
		Info: data.DeviceInfo{
			ProtocolVersion: &protoV1,
		},
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	path1 := "screeny:metaballs"
	app1 := data.App{
		DeviceID: deviceID,
		Iname:    "meta_ws",
		Name:     "Metaballs",
		Enabled:  true,
		Path:     &path1,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app1))
	device.Apps = []*data.App{&app1}

	// 2 rotation chunks
	chunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 50},
		{WebPBytes: dummyChunkWebP2, DurationMs: 50},
	}
	s.RegisterExternalProvider(&mockExternalProvider{name: "screeny", chunks: chunks})
	require.NoError(t, s.saveAppChunks(deviceID, &app1, chunks))

	ts := httptest.NewServer(s)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/" + deviceID + "/ws"
	dialer := websocket.Dialer{}
	conn, resp, err := dialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)

	assert.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))

	// Read messages from WS helper
	readNextBinary := func() []byte {
		for {
			msgType, data, err := conn.ReadMessage()
			require.NoError(t, err)
			if msgType == websocket.BinaryMessage {
				return data
			}
		}
	}

	// Drain initial rotation chunk sent upon WebSocket connect
	initChunk := readNextBinary()
	assert.Equal(t, dummyChunkWebP1, initChunk, "Must receive initial rotation chunk on connect")

	// Broadcast preview message with distinct chunks
	previewChunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP2, DurationMs: 50},
		{WebPBytes: dummyChunkWebP3, DurationMs: 50},
	}
	s.Broadcaster.Notify(deviceID, DevicePreviewMessage{
		App:    &app1,
		Chunks: previewChunks,
	})

	// First binary message after preview broadcast must be preview chunk 0
	b0 := readNextBinary()
	assert.Equal(t, dummyChunkWebP2, b0, "WS client must receive preview chunk 0 first")

	// Send displaying ACK to trigger chunk 1
	_ = conn.WriteJSON(WSMessage{Displaying: new(int)})

	// Second binary message must be preview chunk 1
	b1 := readNextBinary()
	assert.Equal(t, dummyChunkWebP3, b1, "WS client must receive preview chunk 1 next")
}

func TestScreenyApp_RespectsUIntervalAndNonBlockingRotation(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	user := data.User{Username: "screenyuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	device := data.Device{
		ID:              "dev_screeny_loop",
		Username:        user.Username,
		Name:            "Screeny Dev",
		Brightness:      50,
		DefaultInterval: 15,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	// Mock Screeny provider
	renderCallCount := 0
	mock := &mockExternalProvider{
		name: "screeny",
		renderFunc: func(ctx context.Context, req providers.RenderRequest) ([]providers.RenderChunk, error) {
			renderCallCount++
			return []providers.RenderChunk{
				{WebPBytes: dummyChunkWebP1, DurationMs: 15000},
			}, nil
		},
	}
	s.RegisterExternalProvider(mock)

	path := "screeny:flock"
	// App has default UInterval=10 (minutes) as stored by DB on addapp
	app := data.App{
		DeviceID:   device.ID,
		Iname:      "flock_app",
		Name:       "Flock",
		Enabled:    true,
		Path:       &path,
		UInterval:  10,                                // Default 10 minutes from DB
		LastRender: time.Now().Add(-20 * time.Second), // 20s ago
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))

	// 1. App has no cached chunks yet: possiblyRender MUST trigger initial render synchronously
	rendered := s.possiblyRender(ctx, &app, &device, &user)
	assert.True(t, rendered)
	assert.Equal(t, 1, renderCallCount, "Screeny app must render initial batch when uncached")
	assert.True(t, len(s.getAppChunks(device.ID, &app)) > 0, "Cached chunks must exist after initial render")

	// 2. Dwell elapsed (20s > 15s) but UInterval has NOT elapsed (20s < 10m):
	// possiblyRender MUST respect UInterval and NOT re-render
	app.LastRender = time.Now().Add(-20 * time.Second)
	rendered = s.possiblyRender(ctx, &app, &device, &user)
	assert.True(t, rendered)
	assert.Equal(t, 1, renderCallCount, "Screeny app must respect 10m UInterval and not re-render every dwell cycle")

	// 3. Serving via GetNextAppImage on rotation cycles uses cached batch without re-rendering
	device.Apps = []*data.App{&app}
	_, servedApp, err := s.GetNextAppImage(ctx, &device, &user, "")
	require.NoError(t, err)
	require.NotNil(t, servedApp)
	assert.Equal(t, 1, renderCallCount, "Serving rotation uses cached batch without blocking")

	// Subsequent rotation cycle still within UInterval uses cached batch
	_, servedApp2, err := s.GetNextAppImage(ctx, &device, &user, "")
	require.NoError(t, err)
	require.NotNil(t, servedApp2)
	assert.Equal(t, 1, renderCallCount, "Subsequent rotation cycle uses cached batch")

	// 4. Once UInterval has expired (e.g. 11 minutes ago):
	// GetNextAppImage serves cached chunks immediately to avoid freezing rotation,
	// and triggers the interval re-render asynchronously in the background.
	app.LastRender = time.Now().Add(-11 * time.Minute)
	_, servedApp3, err := s.GetNextAppImage(ctx, &device, &user, "")
	require.NoError(t, err)
	require.NotNil(t, servedApp3)

	// Wait for the asynchronous background render to complete
	s.backgroundRenders.Wait()
	assert.Equal(t, 2, renderCallCount, "Screeny app must refresh in the background once UInterval has expired")
}

func TestClockApp_ZeroUIntervalRerendersOnEveryRotation(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()

	user := data.User{Username: "clockuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	device := data.Device{
		ID:              "dev_clock_zero",
		Username:        user.Username,
		Name:            "Clock Dev",
		Brightness:      50,
		DefaultInterval: 15,
	}
	require.NoError(t, gorm.G[data.Device](s.DB).Create(ctx, &device))

	renderCallCount := 0
	mock := &mockExternalProvider{
		name: "screeny",
		renderFunc: func(ctx context.Context, req providers.RenderRequest) ([]providers.RenderChunk, error) {
			renderCallCount++
			return []providers.RenderChunk{
				{WebPBytes: dummyChunkWebP1, DurationMs: 15000},
			}, nil
		},
	}
	s.RegisterExternalProvider(mock)

	path := "screeny:clock"
	// UInterval = 0 for clocks (re-render on every rotation)
	app := data.App{
		DeviceID:   device.ID,
		Iname:      "clock_app",
		Name:       "Clock",
		Enabled:    true,
		Path:       &path,
		UInterval:  0, // Must be respected as 0 (not overridden to 10)
		LastRender: time.Now(),
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))

	// Initial render
	rendered := s.possiblyRender(ctx, &app, &device, &user)
	assert.True(t, rendered)
	assert.Equal(t, 1, renderCallCount)

	// Even if LastRender was just now (0 seconds ago), UInterval=0 means it must re-render on rotation
	device.Apps = []*data.App{&app}
	_, served, err := s.GetNextAppImage(ctx, &device, &user, "")
	require.NoError(t, err)
	require.NotNil(t, served)

	s.backgroundRenders.Wait()
	assert.Equal(t, 2, renderCallCount, "App with UInterval=0 must re-render on every rotation cycle")
}

func TestStarlark_AutoChunking_OversizedAnimation(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "starlarkuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	// ESP32 device with 2,000 byte budget to force chunking on a small animation
	s.Config.MaxChunkSizeESP = 2000

	device := data.Device{
		ID:              "dev_starlark_chunk",
		Username:        user.Username,
		Name:            "Starlark Dev",
		Brightness:      50,
		DefaultInterval: 15,
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
	require.Greater(t, len(rawWebP), 2000, "Encoded animation must exceed the 2000 byte chunk budget")

	appDir := filepath.Join(s.DataDir, "apps", "test_starlark_anim")
	require.NoError(t, os.MkdirAll(appDir, 0755))
	animPath := filepath.Join(appDir, "anim.webp")
	require.NoError(t, os.WriteFile(animPath, rawWebP, 0644))

	relPath := filepath.Join("apps", "test_starlark_anim", "anim.webp")
	app := data.App{
		DeviceID:   device.ID,
		Iname:      "test_anim_iname",
		Name:       "Radar Animation",
		Enabled:    true,
		Path:       &relPath,
		UInterval:  1,
		LastRender: time.Now().Add(-10 * time.Minute),
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))

	// 1. Render via possiblyRender
	rendered := s.possiblyRender(ctx, &app, &device, &user)
	assert.True(t, rendered)

	// 2. Verify chunks were generated and saved
	chunks := s.getAppChunks(device.ID, &app)
	require.GreaterOrEqual(t, len(chunks), 2, "Animation exceeding maxChunkSize must be partitioned into at least 2 chunks")
	maxLimit := s.GetMaxChunkSizeForDevice(&device)
	for idx, chunk := range chunks {
		assert.LessOrEqual(t, len(chunk.WebPBytes), maxLimit, "Chunk %d size (%d) must not exceed maxLimit (%d)", idx, len(chunk.WebPBytes), maxLimit)
		assert.Greater(t, chunk.DurationMs, 0)
	}

	// 3. Verify disk structure: _full.webp, primary .webp, and individual chunk files
	deviceWebpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)

	fullPath := filepath.Join(deviceWebpDir, "Radar Animation-test_anim_iname_full.webp")
	fullBytes, err := os.ReadFile(fullPath)
	require.NoError(t, err, "_full.webp must be saved to disk for rich browser previews")
	require.NotEmpty(t, fullBytes)
	decodedFull, _, err := chunker.DecodeWebPAnimation(fullBytes)
	require.NoError(t, err)
	assert.Len(t, decodedFull, 40, "_full.webp must contain all 40 frames")

	primaryPath := filepath.Join(deviceWebpDir, "Radar Animation-test_anim_iname.webp")
	primaryBytes, err := os.ReadFile(primaryPath)
	require.NoError(t, err)
	assert.Equal(t, chunks[0].WebPBytes, primaryBytes, "Primary .webp file must contain chunk 0 (safe for device)")

	metaPath := filepath.Join(deviceWebpDir, "Radar Animation-test_anim_iname_chunks.json")
	assert.FileExists(t, metaPath, "_chunks.json manifest must exist")

	// 4. Test sequential HTTP rotation across chunks
	device.Apps = []*data.App{&app}
	c0Bytes, servedApp, dwell0, err := s.GetNextAppImageHTTP(ctx, &device, &user, "")
	require.NoError(t, err)
	assert.Equal(t, app.Iname, servedApp.Iname)
	assert.Equal(t, chunks[0].WebPBytes, c0Bytes)
	assert.Greater(t, dwell0, 0)

	// Second poll serves chunk 1
	c1Bytes, servedApp, dwell1, err := s.GetNextAppImageHTTP(ctx, &device, &user, "")
	require.NoError(t, err)
	assert.Equal(t, app.Iname, servedApp.Iname)
	assert.Equal(t, chunks[1].WebPBytes, c1Bytes)
	assert.Greater(t, dwell1, 0)
}

func TestStarlark_AutoChunking_PushPreview(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	user := data.User{Username: "previewuser"}
	require.NoError(t, gorm.G[data.User](s.DB).Create(ctx, &user))

	s.Config.MaxChunkSizeESP = 2000

	device := data.Device{
		ID:              "dev_preview_chunk",
		Username:        user.Username,
		Name:            "Preview Dev",
		Brightness:      50,
		DefaultInterval: 15,
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
