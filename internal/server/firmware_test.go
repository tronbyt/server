package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/firmware"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleFirmwareGenerateGet(t *testing.T) {
	s := newTestServerAPI(t)

	var user data.User
	s.DB.First(&user, "username = ?", "testuser")
	var device data.Device
	s.DB.First(&device, "id = ?", "testdevice")

	req := httptest.NewRequest(http.MethodGet, "/devices/testdevice/firmware", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	s.handleFirmwareGenerateGet(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}
}

func TestHandleFirmwareGeneratePost(t *testing.T) {
	s := newTestServerAPI(t)
	var device data.Device
	s.DB.First(&device, "id = ?", "testdevice")
	var user data.User
	s.DB.First(&user, "username = ?", "testuser")

	dummyFirmware := make([]byte, 1024)
	copy(dummyFirmware, []byte("dummy data"))

	ssidPlaceholder := firmware.PlaceholderSSID + "\x00"
	copy(dummyFirmware[100:], []byte(ssidPlaceholder))

	passPlaceholder := firmware.PlaceholderPassword + "\x00"
	copy(dummyFirmware[200:], []byte(passPlaceholder))

	urlPlaceholder := firmware.PlaceholderURL + "\x00"
	copy(dummyFirmware[300:], []byte(urlPlaceholder))

	firmwareDir := filepath.Join(s.DataDir, "firmware")
	releasesDir := filepath.Join(firmwareDir, "releases", "v1.0.0")
	if err := os.MkdirAll(releasesDir, 0755); err != nil {
		t.Fatalf("Failed to create firmware releases directory: %v", err)
	}

	if err := os.WriteFile(filepath.Join(releasesDir, "tidbyt-gen1.bin"), dummyFirmware, 0644); err != nil {
		t.Fatalf("Failed to write dummy firmware file: %v", err)
	}

	// Create a dummy merged firmware file (at least MergedAppOffset = 0x10000 bytes)
	mergedFirmware := make([]byte, 0x10000+len(dummyFirmware))
	if err := os.WriteFile(filepath.Join(releasesDir, "tidbyt-gen1_merged.bin"), mergedFirmware, 0644); err != nil {
		t.Fatalf("Failed to write dummy merged firmware file: %v", err)
	}

	form := url.Values{}
	form.Add("wifi_ap", "TestSSID")
	form.Add("wifi_password", "TestPass")
	form.Add("img_url", "http://example.com/image")

	req := httptest.NewRequest(http.MethodPost, "/devices/testdevice/firmware", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	s.handleFirmwareGeneratePost(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}

	if rr.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Expected content type application/octet-stream, got %s", rr.Header().Get("Content-Type"))
	}

	if rr.Body.Len() == 0 {
		t.Error("Expected firmware binary in response")
	}
}

func TestUpdateFirmwareBinariesRejectsHTMLAndRetriesAPI(t *testing.T) {
	s := newTestServerAPI(t)
	s.Config.GitHubToken = "test-token"
	t.Setenv("FIRMWARE_REPO", "")

	firmwareBytes := make([]byte, 64)
	firmwareBytes[0] = 0xE9

	var browserHits, apiHits int
	var apiAccept, apiAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/tronbyt/firmware-esp32/releases"):
			base := "http://" + r.Host
			w.Header().Set("Content-Type", "application/json")
			if _, err := fmt.Fprintf(w, `[{"tag_name":"v9.9.9","assets":[{"name":"tidbyt-gen1_firmware.bin","url":"%s/api-asset","browser_download_url":"%s/browser"}]}]`, base, base); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		case r.URL.Path == "/browser":
			browserHits++
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<!DOCTYPE html><html><body>Sign in to GitHub</body></html>"))
		case r.URL.Path == "/api-asset":
			apiHits++
			apiAccept = r.Header.Get("Accept")
			apiAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(firmwareBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	s.githubAPIBase = ts.URL

	require.NoError(t, s.UpdateFirmwareBinaries(1))

	assert.Equal(t, 1, browserHits)
	assert.Equal(t, 1, apiHits, "API asset endpoint should be retried after HTML login response")
	assert.Equal(t, "application/octet-stream", apiAccept)
	assert.Equal(t, "Bearer test-token", apiAuth)

	localPath := filepath.Join(s.DataDir, "firmware", "releases", "v9.9.9", "tidbyt-gen1.bin")
	got, err := os.ReadFile(localPath)
	require.NoError(t, err)
	assert.Equal(t, firmwareBytes, got)

	_, err = os.Stat(localPath + ".tmp")
	assert.True(t, os.IsNotExist(err), "temporary file should be cleaned up")

	// release.json should be saved
	releaseJSONPath := filepath.Join(s.DataDir, "firmware", "releases", "v9.9.9", "release.json")
	assert.FileExists(t, releaseJSONPath)
}

func TestGetFirmwareReleaseInfo_Cached(t *testing.T) {
	s := newTestServerAPI(t)

	verDir := filepath.Join(s.DataDir, "firmware", "releases", "v1.2.3")
	require.NoError(t, os.MkdirAll(verDir, 0755))

	releaseJSON := `{"tag_name":"v1.2.3","name":"Release 1.2.3","body":"## What's Changed\n* Added test feature by @tester in https://github.com/tronbyt/firmware-esp32/pull/1","html_url":"https://github.com/tronbyt/firmware-esp32/releases/tag/v1.2.3"}`
	require.NoError(t, os.WriteFile(filepath.Join(verDir, "release.json"), []byte(releaseJSON), 0644))

	info, err := s.GetFirmwareReleaseInfo("v1.2.3")
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "v1.2.3", info.TagName)
	assert.Equal(t, "Release 1.2.3", info.Name)
	assert.Contains(t, info.Body, "Added test feature")
	assert.Equal(t, "https://github.com/tronbyt/firmware-esp32/releases/tag/v1.2.3", info.HTMLURL)
}

func TestGetFirmwareReleaseInfo_FetchAndCache(t *testing.T) {
	s := newTestServerAPI(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/releases/tags/v2.0.0") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"tag_name":"v2.0.0","name":"v2.0.0","body":"## What's Changed\n* Major update","html_url":"https://github.com/tronbyt/firmware-esp32/releases/tag/v2.0.0"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	s.githubAPIBase = ts.URL

	info, err := s.GetFirmwareReleaseInfo("v2.0.0")
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "v2.0.0", info.TagName)
	assert.Contains(t, info.Body, "Major update")

	// Verify it was cached to disk
	cachedPath := filepath.Join(s.DataDir, "firmware", "releases", "v2.0.0", "release.json")
	assert.FileExists(t, cachedPath)
}

func TestHandleFirmwareReleaseNotesEndpoint(t *testing.T) {
	s := newTestServerAPI(t)

	var user data.User
	s.DB.First(&user, "username = ?", "testuser")
	var device data.Device
	s.DB.First(&device, "id = ?", "testdevice")

	verDir := filepath.Join(s.DataDir, "firmware", "releases", "v1.5.0")
	require.NoError(t, os.MkdirAll(verDir, 0755))
	releaseJSON := `{"tag_name":"v1.5.0","name":"v1.5.0","body":"## What's Changed\n* Sound fixes","html_url":"https://github.com/tronbyt/firmware-esp32/releases/tag/v1.5.0"}`
	require.NoError(t, os.WriteFile(filepath.Join(verDir, "release.json"), []byte(releaseJSON), 0644))

	// 1. Missing version parameter
	req := httptest.NewRequest(http.MethodGet, "/devices/testdevice/firmware/release_notes", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	rr := httptest.NewRecorder()
	s.handleFirmwareReleaseNotes(rr, req.WithContext(ctx))
	assert.Equal(t, http.StatusBadRequest, rr.Code)

	// 2. Valid request
	req = httptest.NewRequest(http.MethodGet, "/devices/testdevice/firmware/release_notes?version=v1.5.0", nil)
	rr = httptest.NewRecorder()
	s.handleFirmwareReleaseNotes(rr, req.WithContext(ctx))
	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Contains(t, rr.Body.String(), `"tag_name":"v1.5.0"`)
	assert.Contains(t, rr.Body.String(), "Sound fixes")
}

func TestGetAvailableFirmwareVersionsForDevice(t *testing.T) {
	s := newTestServerAPI(t)

	// Create v1.0.0 with tidbyt-gen1 binary
	v1Dir := filepath.Join(s.DataDir, "firmware", "releases", "v1.0.0")
	require.NoError(t, os.MkdirAll(v1Dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(v1Dir, "tidbyt-gen1.bin"), []byte("bin-data"), 0644))

	// Create v2.0.0 with tronberry binary
	v2Dir := filepath.Join(s.DataDir, "firmware", "releases", "v2.0.0")
	require.NoError(t, os.MkdirAll(v2Dir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(v2Dir, "tronberry"), []byte("elf-data"), 0644))

	gen1Dev := &data.Device{Type: data.DeviceTidbytGen1}
	piDev := &data.Device{Type: data.DeviceRaspberryPi}

	gen1Vers := s.GetAvailableFirmwareVersionsForDevice(gen1Dev)
	assert.Equal(t, []string{"v1.0.0"}, gen1Vers)
	assert.Equal(t, "v1.0.0", s.GetLatestFirmwareVersionForDevice(gen1Dev))

	piVers := s.GetAvailableFirmwareVersionsForDevice(piDev)
	assert.Equal(t, []string{"v2.0.0"}, piVers)
	assert.Equal(t, "v2.0.0", s.GetLatestFirmwareVersionForDevice(piDev))

	allVers := s.GetAvailableFirmwareVersionsForDevice(nil)
	assert.Len(t, allVers, 2)
}

func TestUpdateFirmwareBinariesDownloadsTronberry(t *testing.T) {
	s := newTestServerAPI(t)
	t.Setenv("FIRMWARE_REPO", "none") // Skip ESP32
	t.Setenv("TRONBERRY_REPO", "")

	tronberryELF := []byte{0x7f, 'E', 'L', 'F', 0x02, 0x01, 0x01, 0x00}
	tronberryELF = append(tronberryELF, make([]byte, 1024)...)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/repos/tronbyt/tronberry/releases"):
			base := "http://" + r.Host
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `[{"tag_name":"v0.2.0","name":"Tronberry v0.2.0","body":"Release notes","assets":[{"name":"tronberry","url":"%s/api-asset","browser_download_url":"%s/browser"}]}]`, base, base)
		case r.URL.Path == "/browser":
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(tronberryELF)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	s.githubAPIBase = ts.URL

	require.NoError(t, s.UpdateFirmwareBinaries(1))

	localPath := filepath.Join(s.DataDir, "firmware", "releases", "v0.2.0", "tronberry")
	got, err := os.ReadFile(localPath)
	require.NoError(t, err)
	assert.Equal(t, tronberryELF, got)

	// Device query should find v0.2.0
	piDev := &data.Device{Type: data.DeviceRaspberryPi}
	assert.Equal(t, []string{"v0.2.0"}, s.GetAvailableFirmwareVersionsForDevice(piDev))
}
