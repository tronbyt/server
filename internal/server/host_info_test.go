package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/version"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestNormalizeVersion(t *testing.T) {
	for in, want := range map[string]string{
		"":        "",
		"unknown": "",
		"2.6.0":   "v2.6.0",
		"v2.6.0":  "v2.6.0",
		" 1.7.4 ": "v1.7.4",
		"dev":     "dev",
	} {
		assert.Equal(t, want, normalizeVersion(in), "normalizeVersion(%q)", in)
	}
}

// TestRenderAppReadsHostInfo checks that apps can read the server, app store
// and device details through Pixlet's server.star and device.star modules.
func TestRenderAppReadsHostInfo(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()

	oldVersion := version.Version
	version.Version = "2.6.0"
	t.Cleanup(func() { version.Version = oldVersion })

	commitTime := time.Date(2026, 11, 10, 4, 38, 0, 0, time.UTC)
	s.systemAppsCacheMutex.Lock()
	s.systemAppsCommit = "5fb4e4ba6"
	s.systemAppsCommitTime = commitTime
	s.systemAppsCacheMutex.Unlock()

	_, err := gorm.G[data.Device](s.DB).Where("id = ?", "testdevice").Updates(ctx, data.Device{
		Type: data.DeviceTidbytGen2,
		Info: data.DeviceInfo{FirmwareVersion: "1.7.4", FirmwareType: "ESP32"},
	})
	require.NoError(t, err)
	dev, err := gorm.G[data.Device](s.DB).Where("id = ?", "testdevice").First(ctx)
	require.NoError(t, err)

	appPath := filepath.Join(t.TempDir(), "hostapp.star")
	require.NoError(t, os.WriteFile(appPath, []byte(`
load("device.star", "device")
load("render.star", "render")
load("server.star", "server")

def check(name, got, want):
    if got != want:
        fail("%s: got %r, want %r" % (name, got, want))

def main(config):
    check("server.version", server.version(), "v2.6.0")
    check("server.system_apps_commit", server.system_apps_commit(), "5fb4e4ba6")
    check("server.system_apps_commit_time", server.system_apps_commit_time().year, 2026)
    check("device.model", device.model(), "tidbyt_gen2")
    check("device.firmware_version", device.firmware_version(), "v1.7.4")
    check("device.firmware_type", device.firmware_type(), "esp32")
    return render.Root(child = render.Box())
`), 0o644))

	img, _, err := s.RenderApp(ctx, &dev, nil, appPath, nil)
	require.NoError(t, err)
	assert.NotEmpty(t, img)

	// Without a device, only the server details are known.
	info := s.hostInfo(nil)
	assert.Equal(t, "v2.6.0", info.ServerVersion)
	assert.Empty(t, info.Device.Model)
	assert.Empty(t, info.Device.FirmwareVersion)
}

func TestHostInfoUnknownModel(t *testing.T) {
	s := newTestServerAPI(t)
	for _, dt := range []data.DeviceType{data.DeviceUnknown, data.DeviceOther} {
		info := s.hostInfo(&data.Device{Type: dt})
		assert.Empty(t, info.Device.Model, "model for %v", dt)
	}
	assert.Equal(t, "raspberrypi", s.hostInfo(&data.Device{Type: data.DeviceRaspberryPi}).Device.Model)
}
