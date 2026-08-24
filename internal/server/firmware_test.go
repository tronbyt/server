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
}
