package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/providers"
)

func TestAppWebpNameFlattensSeparators(t *testing.T) {
	pushedPath := "pushed:../../victim"
	tests := []struct {
		name string
		app  data.App
		want string
	}{
		{"plain", data.App{Name: "clock", Iname: "100"}, "clock-100.webp"},
		{"slash in name", data.App{Name: "AC/DC", Iname: "100"}, "AC_DC-100.webp"},
		{"traversal name", data.App{Name: "../../other/x", Iname: "100"}, ".._.._other_x-100.webp"},
		{"traversal iname", data.App{Name: "x", Iname: `..\..\y`}, "x-.._.._y.webp"},
		{"pushed traversal", data.App{Pushed: true, Path: &pushedPath}, "pushed/.._.._victim.webp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, appWebpName(&tt.app))
		})
	}
}

// App names and imported inames are user-controlled; renders built from them
// must stay inside the device's own image directory.
func TestAppChunksStayInDeviceImageDir(t *testing.T) {
	s := newTestServerAPI(t)
	deviceID := "testdevice"

	webpDir, err := s.ensureDeviceImageDir(deviceID)
	require.NoError(t, err)

	// Another device's render, reachable from testdevice via "../victimdevice".
	victimDir, err := s.ensureDeviceImageDir("victimdevice")
	require.NoError(t, err)
	victim := filepath.Join(victimDir, "x-100.webp")
	require.NoError(t, os.WriteFile(victim, []byte("victim"), 0644))

	app := data.App{Name: "../victimdevice/x", Iname: "100"}
	chunks := []providers.RenderChunk{
		{WebPBytes: []byte("chunk0"), FullWebPBytes: []byte("full"), DurationMs: 1000},
		{WebPBytes: []byte("chunk1"), DurationMs: 1000},
	}
	require.NoError(t, s.saveAppChunks(deviceID, &app, chunks))

	b, err := os.ReadFile(victim)
	require.NoError(t, err)
	assert.Equal(t, "victim", string(b), "save must not overwrite another device's render")
	assert.FileExists(t, filepath.Join(webpDir, appWebpName(&app)))
	assert.FileExists(t, filepath.Join(webpDir, appFullWebpName(&app)))

	s.invalidateAppChunks(deviceID, app.Iname)
	loaded := s.getAppChunks(deviceID, &app)
	require.Len(t, loaded, 2)
	assert.Equal(t, "chunk1", string(loaded[1].WebPBytes))

	s.cleanupAppChunks(deviceID, &app)
	assert.FileExists(t, victim, "cleanup must not delete another device's render")
	assert.NoFileExists(t, filepath.Join(webpDir, appFullWebpName(&app)))
}

func TestDeviceImageRootRejectsSymlinkEscape(t *testing.T) {
	s := newTestServerAPI(t)
	deviceID := "testdevice"

	webpDir, err := s.ensureDeviceImageDir(deviceID)
	require.NoError(t, err)

	secret := filepath.Join(s.DataDir, "secret.db")
	require.NoError(t, os.WriteFile(secret, []byte("secret"), 0644))

	app := data.App{Name: "x", Iname: "100"}
	require.NoError(t, os.Symlink(secret, filepath.Join(webpDir, appWebpName(&app))))

	_, err = s.readDeviceImage(deviceID, appWebpName(&app))
	require.Error(t, err)

	rr := httptest.NewRecorder()
	assert.False(t, s.serveDeviceImage(rr, httptest.NewRequest(http.MethodGet, "/", nil), deviceID, appWebpName(&app)))
	assert.NotContains(t, rr.Body.String(), "secret")
}
