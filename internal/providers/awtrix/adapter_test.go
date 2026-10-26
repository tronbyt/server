package awtrix

import (
	"context"
	"encoding/json"
	"image"
	"net/http"
	"net/http/httptest"
	"testing"

	"tronbyt-server/internal/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAwtrixAdapter_ListApps(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/apps", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]awtrixAppItem{
			{
				Name:    "Time",
				Enabled: true,
				InLoop:  true,
				Present: true,
				Origin:  "builtin",
				Config:  true,
			},
			{
				Name:    "Date",
				Enabled: true,
				InLoop:  true,
				Present: true,
				Origin:  "builtin",
				Config:  true,
			},
		})
	}))
	defer server.Close()

	adapter := NewAdapter(AdapterConfig{
		BaseURL: server.URL,
	})

	assert.Equal(t, "awtrix", adapter.Name())

	apps, err := adapter.ListApps(context.Background())
	require.NoError(t, err)
	require.Len(t, apps, 2)
	assert.Equal(t, "Time", apps[0].ID)
	assert.Equal(t, "Time", apps[0].Name)
	assert.Equal(t, "Date", apps[1].ID)
}

func TestAwtrixAdapter_GetAppSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/apps/builtin/Time/config", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AwtrixConfigResponse{
			Name: "Time",
			Fields: []AwtrixField{
				{
					Key:     "timeMode",
					Type:    "select",
					Options: []any{0, 1, 2},
					Default: 1,
					Value:   1,
				},
				{
					Key:     "time24h",
					Type:    "bool",
					Default: true,
					Value:   true,
				},
				{
					Key:     "timeColor",
					Type:    "color",
					Default: nil,
				},
			},
		})
	}))
	defer server.Close()

	adapter := NewAdapter(AdapterConfig{
		BaseURL: server.URL,
	})

	schemaBytes, err := adapter.GetAppSchema(context.Background(), "Time")
	require.NoError(t, err)

	var schema PixletSchema
	require.NoError(t, json.Unmarshal(schemaBytes, &schema))
	assert.Equal(t, "1", schema.Version)
	require.Len(t, schema.Schema, 4)

	assert.Equal(t, "timeMode", schema.Schema[0].ID)
	assert.Equal(t, "Clock layout", schema.Schema[0].Name)
	assert.Equal(t, "dropdown", schema.Schema[0].Type)
	assert.Equal(t, "time24h", schema.Schema[1].ID)
	assert.Equal(t, "onoff", schema.Schema[1].Type)
	assert.Equal(t, "timeColor", schema.Schema[2].ID)
	assert.Equal(t, "color", schema.Schema[2].Type)
	assert.Equal(t, "_layout", schema.Schema[3].ID)
	assert.Equal(t, "dropdown", schema.Schema[3].Type)
}

func TestAwtrixAdapter_RenderAppChunks(t *testing.T) {
	activated := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/active":
			assert.Equal(t, http.MethodPut, r.Method)
			activated = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/v1/display/screen":
			assert.Equal(t, http.MethodGet, r.Method)
			w.Header().Set("Content-Type", "application/json")
			// Return a 64x32 dummy screen
			pixels := make([]int, 64*32)
			for i := range pixels {
				pixels[i] = 0x00FF00 // green
			}
			_ = json.NewEncoder(w).Encode(screenData{
				Width:  64,
				Height: 32,
				Pixels: pixels,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter := NewAdapter(AdapterConfig{
		BaseURL: server.URL,
		FPS:     10,
	})

	req := providers.RenderRequest{
		AppID:        "Time",
		Width:        64,
		Height:       32,
		FPS:          10,
		DurationMs:   300, // 3 frames
		MaxChunkSize: 524288,
	}

	chunks, err := adapter.RenderAppChunks(context.Background(), req)
	require.NoError(t, err)
	assert.True(t, activated, "App should have been activated via PUT")
	require.NotEmpty(t, chunks)
	assert.NotEmpty(t, chunks[0].WebPBytes)
}

func TestConvertUserConfigToAwtrix(t *testing.T) {
	fields := []AwtrixField{
		{
			Key:  "time24h",
			Type: "bool",
		},
		{
			Key:  "timeColor",
			Type: "color",
		},
		{
			Key:     "timeMode",
			Type:    "select",
			Options: []any{float64(0), float64(1)},
		},
		{
			Key:  "weekdayBar.show",
			Path: []string{"weekdayBar", "show"},
			Type: "bool",
		},
	}

	userConfig := map[string]any{
		"time24h":         "true",
		"timeColor":       "#ff0000",
		"timeMode":        "1",
		"weekdayBar.show": true,
	}

	converted := ConvertUserConfigToAwtrix(fields, userConfig)

	assert.Equal(t, true, converted["time24h"])
	assert.Equal(t, 16711680, converted["timeColor"])
	assert.Equal(t, 1, converted["timeMode"])

	wb, ok := converted["weekdayBar"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, wb["show"])
}

func TestDetectLayoutInfo_8RowWithCustomBackground(t *testing.T) {
	// Create 64x32 image with High Speed Train's background color 0x05070D
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = 5
		img.Pix[i+1] = 7
		img.Pix[i+2] = 13
		img.Pix[i+3] = 0xFF
	}
	// Draw track on y=6, 7 across x=0..31
	for x := range 32 {
		off6 := img.PixOffset(x, 6)
		img.Pix[off6] = 0x47
		img.Pix[off6+1] = 0x4E
		img.Pix[off6+2] = 0x5C

		off7 := img.PixOffset(x, 7)
		img.Pix[off7] = 0x26
		img.Pix[off7+1] = 0x19
		img.Pix[off7+2] = 0x10
	}

	info, ok := detectLayoutInfo(img)
	require.True(t, ok)
	assert.Equal(t, "8row", info.mode)
	assert.Equal(t, [4]byte{5, 7, 13, 0xFF}, info.bgColor)

	dst := applyAwtrixLayout(img, info, "center")
	require.NotNil(t, dst)
	assert.Equal(t, 64, dst.Bounds().Dx())
	assert.Equal(t, 32, dst.Bounds().Dy())

	// Top rows (y=0..7) and bottom rows (y=24..31) must be background color
	topOff := dst.PixOffset(10, 2)
	assert.Equal(t, uint8(5), dst.Pix[topOff])
	assert.Equal(t, uint8(7), dst.Pix[topOff+1])
	assert.Equal(t, uint8(13), dst.Pix[topOff+2])

	botOff := dst.PixOffset(10, 28)
	assert.Equal(t, uint8(5), dst.Pix[botOff])
	assert.Equal(t, uint8(7), dst.Pix[botOff+1])
	assert.Equal(t, uint8(13), dst.Pix[botOff+2])

	// Track scaled 2x centered at y=8 + 6*2 = 20
	trackOff := dst.PixOffset(0, 20)
	assert.Equal(t, uint8(0x47), dst.Pix[trackOff])
	assert.Equal(t, uint8(0x4E), dst.Pix[trackOff+1])
	assert.Equal(t, uint8(0x5C), dst.Pix[trackOff+2])

	// Right edge of track at x=63 (31*2 + 1)
	trackRightOff := dst.PixOffset(63, 20)
	assert.Equal(t, uint8(0x47), dst.Pix[trackRightOff])
}

func TestDetectLayoutInfo_16RowNarrow(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	// Draw content in 52x16 area
	for y := range 16 {
		for x := range 52 {
			off := img.PixOffset(x, y)
			img.Pix[off] = 0xFF
			img.Pix[off+1] = 0xAA
			img.Pix[off+2] = 0x55
			img.Pix[off+3] = 0xFF
		}
	}

	info, ok := detectLayoutInfo(img)
	require.True(t, ok)
	assert.Equal(t, "16row-narrow", info.mode)
	assert.Equal(t, [4]byte{0, 0, 0, 0xFF}, info.bgColor)

	dst := applyAwtrixLayout(img, info, "center")
	// Content centered at x=6..57, y=8..23
	contentOff := dst.PixOffset(6, 8)
	assert.Equal(t, uint8(0xFF), dst.Pix[contentOff])

	borderOff := dst.PixOffset(2, 4)
	assert.Equal(t, uint8(0), dst.Pix[borderOff])
}

func TestDetectLayoutInfo_32RowFull(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	// Draw content that reaches row 31
	off := img.PixOffset(10, 31)
	img.Pix[off] = 0xFF

	info, ok := detectLayoutInfo(img)
	require.True(t, ok)
	assert.Equal(t, "32row", info.mode)

	dst := applyAwtrixLayout(img, info, "center")
	assert.Equal(t, img, dst, "32row should return src unmodified")
}

func TestApplyAwtrixLayout_Fill(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		srcW, srcH int
		fillByte   byte
	}{
		{name: "8row", mode: "8row", srcW: 32, srcH: 8, fillByte: 0xAA},
		{name: "16row-narrow", mode: "16row-narrow", srcW: 52, srcH: 16, fillByte: 0x11},
		{name: "16row-full", mode: "16row-full", srcW: 64, srcH: 16, fillByte: 0x44},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 64, 32))
			for y := range tc.srcH {
				for x := range tc.srcW {
					off := img.PixOffset(x, y)
					img.Pix[off] = tc.fillByte
					img.Pix[off+1] = tc.fillByte
					img.Pix[off+2] = tc.fillByte
					img.Pix[off+3] = 0xFF
				}
			}

			info := layoutInfo{mode: tc.mode, bgColor: [4]byte{0, 0, 0, 0xFF}}
			dst := applyAwtrixLayout(img, info, "fill")
			require.NotNil(t, dst)
			assert.Equal(t, 64, dst.Bounds().Dx())
			assert.Equal(t, 32, dst.Bounds().Dy())

			// Both corners must be filled with content
			assert.Equal(t, tc.fillByte, dst.Pix[dst.PixOffset(0, 0)])
			assert.Equal(t, tc.fillByte, dst.Pix[dst.PixOffset(63, 31)])
		})
	}
}

func TestFitToTargetCanvas_Tronberry128x64(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	img.Pix[0] = 0x99

	info := layoutInfo{mode: "32row", bgColor: [4]byte{0, 0, 0, 0xFF}}
	dst := fitToTargetCanvas(img, 128, 64, "fill", info, "nearest")
	require.NotNil(t, dst)
	assert.Equal(t, 128, dst.Bounds().Dx())
	assert.Equal(t, 64, dst.Bounds().Dy())
}

func TestFitToTargetCanvas_Square64x64_Center(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xAA
		img.Pix[i+1] = 0xBB
		img.Pix[i+2] = 0xCC
		img.Pix[i+3] = 0xFF
	}

	info := layoutInfo{mode: "32row", bgColor: [4]byte{1, 2, 3, 0xFF}}
	dst := fitToTargetCanvas(img, 64, 64, "center", info, "nearest")
	require.NotNil(t, dst)
	assert.Equal(t, 64, dst.Bounds().Dx())
	assert.Equal(t, 64, dst.Bounds().Dy())

	// Top rows (y=0..15) must be background color
	topOff := dst.PixOffset(10, 5)
	assert.Equal(t, uint8(1), dst.Pix[topOff])
	assert.Equal(t, uint8(2), dst.Pix[topOff+1])

	// Centered content (y=16..47)
	midOff := dst.PixOffset(10, 20)
	assert.Equal(t, uint8(0xAA), dst.Pix[midOff])

	// Bottom rows (y=48..63) must be background color
	botOff := dst.PixOffset(10, 55)
	assert.Equal(t, uint8(1), dst.Pix[botOff])
}

func TestFitToTargetCanvas_Square64x64_Fill(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = 0xAA
		img.Pix[i+1] = 0xBB
		img.Pix[i+2] = 0xCC
		img.Pix[i+3] = 0xFF
	}

	info := layoutInfo{mode: "32row", bgColor: [4]byte{0, 0, 0, 0xFF}}
	dst := fitToTargetCanvas(img, 64, 64, "fill", info, "nearest")
	require.NotNil(t, dst)
	assert.Equal(t, 64, dst.Bounds().Dx())
	assert.Equal(t, 64, dst.Bounds().Dy())

	// Entire canvas should be filled with content
	topOff := dst.PixOffset(0, 0)
	assert.Equal(t, uint8(0xAA), dst.Pix[topOff])
	botOff := dst.PixOffset(63, 63)
	assert.Equal(t, uint8(0xAA), dst.Pix[botOff])
}

func TestConvertUserConfigToAwtrix_ExcludesInternalKeys(t *testing.T) {
	fields := []AwtrixField{
		{Key: "city", Type: "text"},
	}
	userConfig := map[string]any{
		"city":    "Berlin",
		"_layout": "fill",
	}

	body := ConvertUserConfigToAwtrix(fields, userConfig)
	assert.Equal(t, "Berlin", body["city"])
	assert.Nil(t, body["_layout"], "Internal keys starting with _ must not be passed to Awtrix PATCH")
}

func TestConvertUserConfigToAwtrix_DiscardsUnmappedKeys(t *testing.T) {
	fields := []AwtrixField{
		{Key: "city", Type: "text"},
	}
	userConfig := map[string]any{
		"city":          "London",
		"arbitrary_inj": "evil_payload",
		"nested.key":    true,
	}

	body := ConvertUserConfigToAwtrix(fields, userConfig)
	assert.Equal(t, "London", body["city"])
	assert.NotContains(t, body, "arbitrary_inj", "Unmapped fields must be discarded to prevent parameter injection")
	assert.NotContains(t, body, "nested", "Unmapped nested fields must be discarded")
}

func TestFitToTargetCanvas_ZeroDimensions(t *testing.T) {
	emptyImg := image.NewRGBA(image.Rect(0, 0, 0, 0))
	info := layoutInfo{mode: "32row"}
	assert.NotPanics(t, func() {
		res := fitToTargetCanvas(emptyImg, 64, 32, "fill", info, "bicubic")
		assert.Equal(t, emptyImg, res)
	})

	assert.Nil(t, fitToTargetCanvas(nil, 64, 32, "fill", info, "bicubic"))
	assert.NotPanics(t, func() {
		detectLayoutInfo(emptyImg)
	})
}
