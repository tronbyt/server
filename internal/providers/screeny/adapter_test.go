package screeny

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tronbyt-server/internal/providers"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScreenyAdapter_Lifecycle(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	var setPatchReceived string
	var setSeedReceived uint32
	var failSeed bool
	setParamReceived := make(map[string]float32)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/bootstrap":
			bootstrap := StudioBootstrap{
				Patches: []StudioPatch{
					{
						ID:       "split_flap",
						Name:     "Split-Flap",
						Blurb:    "Airport departure board",
						Playable: true,
						Params: []StudioParam{
							{
								ID:      "style",
								Label:   "Board Style",
								Choices: []string{"Classic", "Modern"},
							},
						},
					},
					{
						ID:       "metaballs",
						Name:     "Metaballs",
						Blurb:    "Procedural blobs",
						Playable: true,
						Seeded:   true,
						Params:   []StudioParam{},
					},
					{
						ID:       "gpu_patch",
						Name:     "GPU Patch",
						Playable: false,
						UnplayableReason: func() *string {
							s := "requires GPU"
							return &s
						}(),
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(bootstrap)

		case "/api/v1/set_patch":
			var req map[string]string
			_ = json.NewDecoder(r.Body).Decode(&req)
			setPatchReceived = req["id"]
			w.WriteHeader(http.StatusOK)

		case "/api/v1/set_seed":
			if failSeed {
				http.Error(w, "internal server error", http.StatusInternalServerError)
				return
			}
			var req struct {
				Seed uint32 `json:"seed"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			setSeedReceived = req.Seed
			w.WriteHeader(http.StatusOK)

		case "/api/v1/set_param":
			var req struct {
				ID    string  `json:"id"`
				Value float32 `json:"value"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			setParamReceived[req.ID] = req.Value
			w.WriteHeader(http.StatusOK)

		case "/api/v1/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()

			// Send 15 preview frames with timer = 5.0s (past the 2.05s crossfade)
			previewPacket := make([]byte, PreviewPacketLen)
			binary.LittleEndian.PutUint32(previewPacket[4:8], math.Float32bits(5.0))
			for p := range NPixels {
				previewPacket[PreviewHeaderLen+p*3] = 100
				previewPacket[PreviewHeaderLen+p*3+1] = 150
				previewPacket[PreviewHeaderLen+p*3+2] = 200
			}

			for range 15 {
				if err := conn.WriteMessage(websocket.BinaryMessage, previewPacket); err != nil {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewAdapter(AdapterConfig{
		StudioURL: server.URL,
		FPS:       30,
	})

	ctx := context.Background()

	// 1. ListApps
	apps, err := adapter.ListApps(ctx)
	require.NoError(t, err)
	require.Len(t, apps, 2, "unplayable patch should be filtered out, 2 playable patches remain")
	assert.Equal(t, "split_flap", apps[0].ID)
	assert.Equal(t, "Split-Flap", apps[0].Name)
	assert.Equal(t, "screeny", apps[0].Provider)
	assert.Equal(t, "metaballs", apps[1].ID)

	// 2. GetAppSchema (default includes scale_filter, unseeded patch excludes seed)
	schemaBytes, err := adapter.GetAppSchema(ctx, "split_flap")
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(schemaBytes), "Board Style"))
	assert.True(t, strings.Contains(string(schemaBytes), "scale_filter"))
	assert.False(t, strings.Contains(string(schemaBytes), "Random Seed"), "unseeded patch must not include seed field")

	// GetAppSchema for seeded patch (metaballs) includes seed field
	schemaSeeded, err := adapter.GetAppSchema(ctx, "metaballs")
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(schemaSeeded), "Random Seed"), "seeded patch must include seed field")

	// GetAppSchema for 64x32 devices (Supports2xKey = false)
	ctxNo2x := context.WithValue(ctx, providers.Supports2xKey, false)
	schemaNo2x, err := adapter.GetAppSchema(ctxNo2x, "split_flap")
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(schemaNo2x), "Board Style"))
	assert.False(t, strings.Contains(string(schemaNo2x), "scale_filter"))

	// 3. RenderAppChunks (128x64 with scale_filter parameter) for unseeded patch
	req := providers.RenderRequest{
		AppID:        "split_flap",
		Width:        128,
		Height:       64,
		FPS:          30,
		DurationMs:   300, // 300ms = 9 frames at 30 fps
		MaxChunkSize: 5242880,
		Config: map[string]any{
			"style":        "1",
			"scale_filter": "scale2x",
		},
	}

	chunks, err := adapter.RenderAppChunks(ctx, req)
	require.NoError(t, err)
	require.NotEmpty(t, chunks)

	assert.Equal(t, "split_flap", setPatchReceived)
	assert.Equal(t, uint32(0), setSeedReceived, "unseeded patch must not call set_seed")
	assert.Equal(t, float32(1.0), setParamReceived["style"])
	assert.NotContains(t, setParamReceived, "scale_filter", "scale_filter should not be sent to screeny set_param")
	assert.True(t, len(chunks[0].WebPBytes) > 0)

	// 4. Subsequent RenderAppChunks with the SAME patch must NOT call set_patch
	// to preserve continuous procedural animation streams (Overland, Knot)
	setPatchReceived = ""
	chunks2, err := adapter.RenderAppChunks(ctx, req)
	require.NoError(t, err)
	require.NotEmpty(t, chunks2)
	assert.Empty(t, setPatchReceived, "set_patch must not be called when patch is already active")

	// 5. Render seeded patch without seed config -> random seed is generated and sent
	reqSeeded := providers.RenderRequest{
		AppID:        "metaballs",
		Width:        64,
		Height:       32,
		FPS:          30,
		DurationMs:   300,
		MaxChunkSize: 5242880,
		Config:       map[string]any{},
	}
	chunks3, err := adapter.RenderAppChunks(ctx, reqSeeded)
	require.NoError(t, err)
	require.NotEmpty(t, chunks3)
	assert.Equal(t, "metaballs", setPatchReceived)
	assert.NotZero(t, setSeedReceived, "seeded patch with no config must be assigned a random non-zero seed")

	// 6. Render seeded patch with explicit fixed seed -> specific seed is set
	reqFixedSeed := providers.RenderRequest{
		AppID:        "metaballs",
		Width:        64,
		Height:       32,
		FPS:          30,
		DurationMs:   300,
		MaxChunkSize: 5242880,
		Config: map[string]any{
			"seed": "42",
		},
	}
	chunks4, err := adapter.RenderAppChunks(ctx, reqFixedSeed)
	require.NoError(t, err)
	require.NotEmpty(t, chunks4)
	assert.Equal(t, uint32(42), setSeedReceived, "fixed seed in config must be applied via set_seed")
	assert.NotContains(t, setParamReceived, "seed", "seed must not be sent to set_param")

	// 7. Changing fixed seed on the same patch -> calls set_seed with the new seed
	reqNewSeed := providers.RenderRequest{
		AppID:        "metaballs",
		Width:        64,
		Height:       32,
		FPS:          30,
		DurationMs:   300,
		MaxChunkSize: 5242880,
		Config: map[string]any{
			"seed": "99",
		},
	}
	chunks5, err := adapter.RenderAppChunks(ctx, reqNewSeed)
	require.NoError(t, err)
	require.NotEmpty(t, chunks5)
	assert.Equal(t, uint32(99), setSeedReceived, "changed fixed seed must be applied via set_seed")

	// 8. Subsequent render with same fixed seed 99 does NOT call set_seed again
	setSeedReceived = 0
	chunks6, err := adapter.RenderAppChunks(ctx, reqNewSeed)
	require.NoError(t, err)
	require.NotEmpty(t, chunks6)
	assert.Equal(t, uint32(0), setSeedReceived, "same fixed seed on active patch should not re-invoke set_seed")

	// 9. When set_seed fails, error is logged and lastSeed is invalidated (reset to 0)
	failSeed = true
	reqFailSeed := providers.RenderRequest{
		AppID:        "metaballs",
		Width:        64,
		Height:       32,
		FPS:          30,
		DurationMs:   300,
		MaxChunkSize: 5242880,
		Config: map[string]any{
			"seed": "101",
		},
	}
	chunks7, err := adapter.RenderAppChunks(ctx, reqFailSeed)
	require.NoError(t, err, "render still proceeds even if seed setting fails")
	require.NotEmpty(t, chunks7)
	adapter.activePatchMu.Lock()
	assert.Equal(t, uint32(0), adapter.lastSeed, "lastSeed must be invalidated to 0 on set_seed failure")
	adapter.activePatchMu.Unlock()
	failSeed = false
}

func TestParseSeedValue(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		wantSeed uint32
		wantOk   bool
	}{
		{"nil", nil, 0, false},
		{"empty string", "", 0, false},
		{"whitespace string", "   ", 0, false},
		{"string random", "random", 0, false},
		{"string auto", "auto", 0, false},
		{"string RANDOM with spaces", "  RANDOM  ", 0, false},
		{"zero int", 0, 0, false},
		{"negative int", -1, 0, false},
		{"negative int64", int64(-50), 0, false},
		{"valid int", 42, 42, true},
		{"valid int32", int32(42), 42, true},
		{"valid int64", int64(42), 42, true},
		{"valid uint", uint(42), 42, true},
		{"valid uint32", uint32(42), 42, true},
		{"valid uint64", uint64(42), 42, true},
		{"valid float64 whole", float64(42), 42, true},
		{"valid float32 whole", float32(42), 42, true},
		{"float with fractional part", float64(42.5), 0, false},
		{"valid json.Number int", json.Number("42"), 42, true},
		{"valid json.Number float whole", json.Number("42.0"), 42, true},
		{"valid string number", "42", 42, true},
		{"valid string with whitespace", "  42  ", 42, true},
		{"max uint32 value", uint32(math.MaxUint32), math.MaxUint32, true},
		{"max uint32 int64", int64(math.MaxUint32), math.MaxUint32, true},
		{"max uint32 uint64", uint64(math.MaxUint32), math.MaxUint32, true},
		{"max uint32 string", "4294967295", math.MaxUint32, true},
		{"max uint32 json.Number", json.Number("4294967295"), math.MaxUint32, true},
		{"overflow int64", int64(math.MaxUint32) + 1, 0, false},
		{"overflow uint64", uint64(math.MaxUint32) + 1, 0, false},
		{"overflow string", "4294967296", 0, false},
		{"overflow json.Number", json.Number("4294967296"), 0, false},
		{"overflow float64", 1e20, 0, false},
		{"NaN float64", math.NaN(), 0, false},
		{"Inf float64", math.Inf(1), 0, false},
		{"negative float64", -1.0, 0, false},
		{"invalid string", "not_a_number", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSeed, gotOk := parseSeedValue(tt.input)
			assert.Equal(t, tt.wantOk, gotOk)
			assert.Equal(t, tt.wantSeed, gotSeed)
		})
	}
}

func TestScreenyAdapter_DiscardsTransitionFrames(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/bootstrap":
			bootstrap := StudioBootstrap{
				Patches: []StudioPatch{
					{ID: "test_patch", Name: "Test Patch", Playable: true},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(bootstrap)

		case "/api/v1/set_patch":
			w.WriteHeader(http.StatusOK)

		case "/api/v1/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer func() { _ = conn.Close() }()

			// 1. Send transition frames (timer = 0.0s and 0.5s < 2.05s) with red pixels
			redPacket0 := make([]byte, PreviewPacketLen)
			binary.LittleEndian.PutUint32(redPacket0[4:8], math.Float32bits(0.0))
			for p := range NPixels {
				redPacket0[PreviewHeaderLen+p*3] = 255 // Red
			}
			_ = conn.WriteMessage(websocket.BinaryMessage, redPacket0)

			redPacket := make([]byte, PreviewPacketLen)
			binary.LittleEndian.PutUint32(redPacket[4:8], math.Float32bits(0.5))
			for p := range NPixels {
				redPacket[PreviewHeaderLen+p*3] = 255 // Red
			}
			for range 5 {
				_ = conn.WriteMessage(websocket.BinaryMessage, redPacket)
				time.Sleep(5 * time.Millisecond)
			}

			// 2. Send 10 post-transition frames (timer = 2.2s >= 2.10s) with green pixels
			greenPacket := make([]byte, PreviewPacketLen)
			binary.LittleEndian.PutUint32(greenPacket[4:8], math.Float32bits(2.2))
			for p := range NPixels {
				greenPacket[PreviewHeaderLen+p*3+1] = 255 // Green
			}
			for range 10 {
				_ = conn.WriteMessage(websocket.BinaryMessage, greenPacket)
				time.Sleep(5 * time.Millisecond)
			}

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewAdapter(AdapterConfig{
		StudioURL:         server.URL,
		FPS:               30,
		TransitionSkipSec: DefaultTransitionSkipSec,
	})

	// Request 5 frames (166ms at 30 fps) with patchChanged=true
	frames, err := adapter.captureFrames(context.Background(), 30, 166, 64, 32, "none", true)
	require.NoError(t, err)
	require.NotEmpty(t, frames)

	// Verify that the captured frames are GREEN (new patch) and NOT RED (transition frame)
	for i, frame := range frames {
		c := frame.RGBAAt(0, 0)
		assert.Equal(t, uint8(0), c.R, "frame %d should not have red from discarded transition frame", i)
		assert.Equal(t, uint8(255), c.G, "frame %d should have green from post-transition frame", i)
	}
}
