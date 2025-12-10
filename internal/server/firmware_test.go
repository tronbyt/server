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

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/firmware"
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
	if err := os.MkdirAll(firmwareDir, 0755); err != nil {
		t.Fatalf("Failed to create firmware directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(firmwareDir, "tidbyt-gen1.bin"), dummyFirmware, 0644); err != nil {
		t.Fatalf("Failed to write dummy firmware file: %v", err)
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
