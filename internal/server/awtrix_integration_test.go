package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/providers"
	"tronbyt-server/internal/providers/awtrix"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAwtrixIntegration(t *testing.T) {
	// Mock Awtrix NG server
	awtrixServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[
				{"name":"Time","enabled":true,"inLoop":true,"present":true,"origin":"builtin","config":true},
				{"name":"Ghost-Parade","enabled":true,"inLoop":true,"present":true,"origin":"script","config":true,"meta":{"name":"Ghost Parade","desc":"Arcade ghosts"}}
			]`))
		case "/api/v1/apps/builtin/Time/config":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"name":"Time",
				"fields":[
					{"key":"time24h","type":"bool","default":true,"value":true},
					{"key":"timeMode","type":"select","options":[0,1,2],"default":1,"value":1}
				]
			}`))
		case "/api/v1/apps/active":
			assert.Equal(t, http.MethodPut, r.Method)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/v1/display/screen":
			w.Header().Set("Content-Type", "application/json")
			pixels := make([]int, 64*32)
			for i := range pixels {
				pixels[i] = 0xFF0000 // Red
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"width":  64,
				"height": 32,
				"pixels": pixels,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer awtrixServer.Close()

	s := newTestServerAPI(t)
	s.Config.AwtrixURL = awtrixServer.URL
	s.Config.AwtrixFPS = 10

	adapter := awtrix.NewAdapter(awtrix.AdapterConfig{
		BaseURL: awtrixServer.URL,
		FPS:     10,
	})
	s.RegisterExternalProvider(adapter)

	// 1. Verify registered
	p := s.GetExternalProvider("awtrix")
	require.NotNil(t, p)
	assert.Equal(t, "awtrix", p.Name())

	// 2. Verify ListApps
	apps, err := p.ListApps(context.Background())
	require.NoError(t, err)
	require.Len(t, apps, 2)
	assert.Equal(t, "Time", apps[0].Name)
	assert.Equal(t, "Ghost Parade", apps[1].Name)

	// 3. Verify parseExternalAppPath
	prov, appID, ok := s.parseExternalAppPath("awtrix:Time")
	assert.True(t, ok)
	assert.Equal(t, "awtrix", prov.Name())
	assert.Equal(t, "Time", appID)

	// 4. Verify RenderAppChunks
	chunks, err := p.RenderAppChunks(context.Background(), providers.RenderRequest{
		AppID:        "Time",
		Width:        64,
		Height:       32,
		FPS:          10,
		DurationMs:   200,
		MaxChunkSize: 160000,
	})
	require.NoError(t, err)
	require.NotEmpty(t, chunks)
	assert.NotEmpty(t, chunks[0].WebPBytes)

	// 5. Verify render in server pipeline
	device := &data.Device{
		ID:   "dev_awtrix",
		Type: data.DeviceTidbytGen1,
	}
	app := &data.App{
		Iname:       "awtrix_clock",
		Name:        "Time",
		Path:        new(string),
		DisplayTime: 5,
		UInterval:   10,
	}
	*app.Path = "awtrix:Time"
	require.NoError(t, s.DB.Create(app).Error)

	rendered := s.possiblyRender(context.Background(), app, device, nil)
	assert.True(t, rendered)

	appChunks := s.getAppChunks(device.ID, app)
	require.NotEmpty(t, appChunks)
	assert.NotEmpty(t, appChunks[0].WebPBytes)
}
