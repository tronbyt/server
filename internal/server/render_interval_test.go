package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Render interval 0 means re-render whenever rotation asks (no minimum wait).
// These tests guard against regressions that map 0 to the default 10 minutes.

func TestHandleConfigAppPost_PreservesZeroRenderInterval(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	require.NoError(t, s.DB.Create(&user).Error)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	require.NoError(t, s.DB.Create(&device).Error)
	app := data.App{
		DeviceID:  device.ID,
		Iname:     "100",
		Name:      "TestApp",
		Enabled:   true,
		UInterval: 15,
	}
	require.NoError(t, s.DB.Create(&app).Error)

	req, err := http.NewRequest(http.MethodPost, "/devices/testdevice/100/config", strings.NewReader(`{"uinterval": 0}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	http.HandlerFunc(s.handleConfigAppPost).ServeHTTP(rr, req)
	require.Equal(t, http.StatusSeeOther, rr.Code)

	var saved data.App
	require.NoError(t, s.DB.First(&saved, "iname = ?", "100").Error)
	assert.Equal(t, 0, saved.UInterval)
}

func TestHandleAddAppPost_PreservesZeroRenderInterval(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	require.NoError(t, s.DB.Create(&user).Error)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	require.NoError(t, s.DB.Create(&device).Error)

	relPath := filepath.Join("users", "testuser", "repo", "apps", "always", "always.star")
	fullPath := filepath.Join(s.DataDir, relPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(fullPath), 0o755))
	require.NoError(t, os.WriteFile(fullPath, []byte(`
load("render.star", "render")
def main(config):
    return render.Root(child=render.Box(width=64, height=32, color="#0000ff"))
`), 0o644))

	form := url.Values{}
	form.Set("name", "Always")
	form.Set("path", relPath)
	form.Set("uinterval", "0")

	req, err := http.NewRequest(http.MethodPost, "/devices/testdevice/addapp", strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	http.HandlerFunc(s.handleAddAppPost).ServeHTTP(rr, req)
	require.Equal(t, http.StatusSeeOther, rr.Code)

	var saved data.App
	require.NoError(t, s.DB.First(&saved, "name = ?", "Always").Error)
	assert.Equal(t, 0, saved.UInterval)
}

func TestPossiblyRender_ZeroRenderIntervalReRendersEveryCycle(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	deviceID := "testdevice"
	var dev data.Device
	require.NoError(t, s.DB.First(&dev, "id = ?", deviceID).Error)
	var user data.User
	require.NoError(t, s.DB.First(&user, "username = ?", dev.Username).Error)

	appRelPath := filepath.Join("system-apps", "apps", "intervalzero", "intervalzero.star")
	appFullPath := filepath.Join(s.DataDir, appRelPath)
	require.NoError(t, os.MkdirAll(filepath.Dir(appFullPath), 0o755))
	star := `
load("render.star", "render")
def main(config):
    return render.Root(child=render.Box(width=64, height=32, color="#00ff00"))
`
	require.NoError(t, os.WriteFile(appFullPath, []byte(star), 0o644))

	t.Run("positive interval skips render until elapsed", func(t *testing.T) {
		app := data.App{
			Iname:      "interval-pos",
			Name:       "intervalzero",
			DeviceID:   deviceID,
			Path:       &appRelPath,
			UInterval:  60,
			Enabled:    true,
			LastRender: time.Now().Add(-time.Minute),
		}
		require.NoError(t, s.DB.Create(&app).Error)
		require.True(t, s.possiblyRender(ctx, &app, &dev, &user))

		before := app.LastRender
		require.True(t, s.possiblyRender(ctx, &app, &dev, &user))

		var persisted data.App
		require.NoError(t, s.DB.First(&persisted, app.ID).Error)
		assert.Equal(t, before.UnixNano(), persisted.LastRender.UnixNano())
	})

	t.Run("zero interval re-renders on every call", func(t *testing.T) {
		app := data.App{
			Iname:      "interval-zero",
			Name:       "intervalzero",
			DeviceID:   deviceID,
			Path:       &appRelPath,
			UInterval:  0,
			Enabled:    true,
			LastRender: time.Now().Add(-time.Minute),
		}
		require.NoError(t, s.DB.Create(&app).Error)
		require.True(t, s.possiblyRender(ctx, &app, &dev, &user))

		before := app.LastRender
		time.Sleep(5 * time.Millisecond)
		require.True(t, s.possiblyRender(ctx, &app, &dev, &user))

		var persisted data.App
		require.NoError(t, s.DB.First(&persisted, app.ID).Error)
		assert.True(t, persisted.LastRender.After(before), "UInterval 0 should refresh LastRender on each possiblyRender call")
	})
}
