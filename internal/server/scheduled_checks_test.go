package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
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

func TestHandleIndexShowsFirmwareUpdateOnButton(t *testing.T) {
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
	assert.Equal(t, 1, strings.Count(body, `class="firmware-update-pill"`))
	assert.Contains(t, body, `href="/devices/dev-old/firmware"`)
	assert.Contains(t, body, "v1.7.5")

	// The single-card refresh used by the dashboard shows it too.
	assert.Contains(t, render("?device_id=dev-old&partial=device_card"), `class="firmware-update-pill"`)
	assert.NotContains(t, render("?device_id=dev-current&partial=device_card"), `class="firmware-update-pill"`)
}

// A shallow check (2 releases) and a deeper one (5 releases) keep separate
// ETags, so the deeper one still downloads the older releases after the
// shallow one has seen the same list.
func TestFirmwareETagIsPerDepth(t *testing.T) {
	s := newTestServer(t)
	t.Setenv("FIRMWARE_REPO", "")
	t.Setenv("TRONBERRY_REPO", "none")

	firmwareBytes := make([]byte, 64)
	firmwareBytes[0] = 0xE9
	const etag = `"releases-v1"`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/tronbyt/firmware-esp32/releases"):
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
			w.Header().Set("Content-Type", "application/json")
			base := "http://" + r.Host
			var releases []string
			for _, v := range []string{"v1.0.3", "v1.0.2", "v1.0.1"} {
				releases = append(releases, fmt.Sprintf(`{"tag_name":%q,"assets":[{"name":"tidbyt-gen2_firmware.bin","browser_download_url":"%s/fw"}]}`, v, base))
			}
			_, _ = w.Write([]byte("[" + strings.Join(releases, ",") + "]"))
		case r.URL.Path == "/fw":
			_, _ = w.Write(firmwareBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	s.githubAPIBase = ts.URL

	released := func(v string) bool {
		_, err := os.Stat(filepath.Join(s.DataDir, "firmware", "releases", v, "tidbyt-gen2.bin"))
		return err == nil
	}

	require.NoError(t, s.UpdateFirmwareBinaries(FirmwareReleasesAtStartup))
	assert.True(t, released("v1.0.2"))
	assert.False(t, released("v1.0.1"), "shallow check downloads only 2 releases")

	require.NoError(t, s.UpdateFirmwareBinaries(FirmwareReleasesToList))
	assert.True(t, released("v1.0.1"), "deeper check must not reuse the shallow check's ETag")
}

// A failed download must not save the ETag, or the next check gets a 304 and
// never retries it.
func TestFirmwareETagNotSavedWhenDownloadFails(t *testing.T) {
	s := newTestServer(t)
	t.Setenv("FIRMWARE_REPO", "")
	t.Setenv("TRONBERRY_REPO", "none")

	firmwareBytes := make([]byte, 64)
	firmwareBytes[0] = 0xE9
	const etag = `"releases-v1"`
	failDownload := true

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/tronbyt/firmware-esp32/releases"):
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `[{"tag_name":"v1.0.3","assets":[{"name":"tidbyt-gen2_firmware.bin","browser_download_url":"http://%s/fw"}]}]`, r.Host)
		case r.URL.Path == "/fw":
			if failDownload {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(firmwareBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	s.githubAPIBase = ts.URL

	firmwarePath := filepath.Join(s.DataDir, "firmware", "releases", "v1.0.3", "tidbyt-gen2.bin")

	require.NoError(t, s.UpdateFirmwareBinaries(FirmwareReleasesAtStartup))
	assert.NoFileExists(t, firmwarePath)

	failDownload = false
	require.NoError(t, s.UpdateFirmwareBinaries(FirmwareReleasesAtStartup))
	assert.FileExists(t, firmwarePath, "next check must retry the failed download")
}

// newAppsSourceRepo makes a local git repository for the system apps refresh
// to clone, with one commit.
func newAppsSourceRepo(t *testing.T) string {
	t.Helper()
	path := t.TempDir()
	r, err := git.PlainInit(path, false)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(path, "README.md"), []byte("apps"), 0o644))
	w, err := r.Worktree()
	require.NoError(t, err)
	_, err = w.Add("README.md")
	require.NoError(t, err)
	_, err = w.Commit("initial", &git.CommitOptions{Author: &object.Signature{Name: "test", Email: "test@example.com", When: time.Now()}})
	require.NoError(t, err)
	return path
}

// When the repository is current but the last cache rebuild failed, the
// scheduled check rebuilds the cache instead of skipping.
func TestRefreshSystemRepoIfChangedRetriesFailedCacheRebuild(t *testing.T) {
	s := newTestServer(t)
	s.Config.SystemAppsRepo = newAppsSourceRepo(t)
	require.NoError(t, s.refreshSystemRepo())
	require.False(t, s.systemAppsCacheStale.Load())

	s.systemAppsCacheStale.Store(true)
	require.NoError(t, s.refreshSystemRepoIfChanged())
	assert.False(t, s.systemAppsCacheStale.Load(), "stale cache should be rebuilt although the repo is unchanged")

	require.NoError(t, s.refreshSystemRepoIfChanged(), "unchanged repo and current cache: nothing to do")
}
