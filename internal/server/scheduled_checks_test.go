package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func addFirmwareRelease(t *testing.T, s *Server, version, binName string) {
	t.Helper()
	dir := filepath.Join(s.DataDir, "firmware", "releases", version)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, binName), []byte("firmware"), 0o644))
}

func TestFirmwareUpdateForDevice(t *testing.T) {
	s := newTestServer(t)
	addFirmwareRelease(t, s, "v1.7.4", "tidbyt-gen2.bin")
	addFirmwareRelease(t, s, "v1.7.5", "tidbyt-gen2.bin")
	addFirmwareRelease(t, s, "v1.8.0", "tronbyt-S3.bin") // not for a Gen 2

	gen2 := func(version string) *data.Device {
		d := &data.Device{Type: data.DeviceTidbytGen2}
		d.Info.FirmwareVersion = version
		return d
	}

	assert.Equal(t, "v1.7.5", s.FirmwareUpdateForDevice(gen2("1.7.0")), "older, without v prefix")
	assert.Equal(t, "v1.7.5", s.FirmwareUpdateForDevice(gen2("v1.7.4")), "older, with v prefix")
	assert.Empty(t, s.FirmwareUpdateForDevice(gen2("1.7.5")), "up to date")
	assert.Empty(t, s.FirmwareUpdateForDevice(gen2("1.8.0")), "newer than any release")
	assert.Empty(t, s.FirmwareUpdateForDevice(gen2("")), "unknown version")
	assert.Empty(t, s.FirmwareUpdateForDevice(gen2("dev-build")), "unparsable version")

	pi := &data.Device{Type: data.DeviceRaspberryPi}
	pi.Info.FirmwareVersion = "1.0.0"
	assert.Empty(t, s.FirmwareUpdateForDevice(pi), "device without Tronbyt firmware")
}

func TestHandleIndexShowsFirmwareUpdateBadge(t *testing.T) {
	s := newTestServer(t)
	addFirmwareRelease(t, s, "v1.7.5", "tidbyt-gen2.bin")

	old := data.Device{ID: "dev-old", Name: "Old", Type: data.DeviceTidbytGen2, APIKey: "dev-key-old"}
	old.Info.FirmwareVersion = "1.7.0"
	current := data.Device{ID: "dev-current", Name: "Current", Type: data.DeviceTidbytGen2, APIKey: "dev-key-current"}
	current.Info.FirmwareVersion = "1.7.5"
	user := data.User{Username: "fwuser", APIKey: "user-key-fw", Devices: []data.Device{old, current}}
	require.NoError(t, s.DB.Create(&user).Error)

	render := func(query string) string {
		req := httptest.NewRequest(http.MethodGet, "/"+query, nil)
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, &user))
		rr := httptest.NewRecorder()
		s.handleIndex(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		return rr.Body.String()
	}

	body := render("")
	assert.Equal(t, 1, strings.Count(body, `class="firmware-update-badge"`))
	assert.Contains(t, body, `href="/devices/dev-old/firmware"`)
	assert.Contains(t, body, "v1.7.5")

	// The single-card refresh used by the dashboard shows it too.
	assert.Contains(t, render("?device_id=dev-old&partial=device_card"), `class="firmware-update-badge"`)
	assert.NotContains(t, render("?device_id=dev-current&partial=device_card"), `class="firmware-update-badge"`)
}
