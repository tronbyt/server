package server

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tronbyt-server/internal/data"
)

type testContextKey string

func TestHandleTriggerOTACustomFirmware(t *testing.T) {
	s := newTestServerAPI(t)
	var device data.Device
	if err := s.DB.First(&device, "id = ?", "testdevice").Error; err != nil {
		t.Fatalf("Failed to find device: %v", err)
	}
	var user data.User
	if err := s.DB.First(&user, "username = ?", "testuser").Error; err != nil {
		t.Fatalf("Failed to find user 'testuser': %v", err)
	}

	// Create a dummy firmware file
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("firmware_file", "custom.bin")
	if err != nil {
		t.Fatalf("Failed to create form file: %v", err)
	}
	content := []byte("custom firmware content")
	if _, err := io.Copy(part, bytes.NewReader(content)); err != nil {
		t.Fatalf("Failed to copy file content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Failed to close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/devices/testdevice/ota", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	// Simulate session for flash messages
	session, _ := s.Store.Get(req, "session-name")
	req = req.WithContext(context.WithValue(req.Context(), testContextKey("session"), session))

	s.handleTriggerOTA(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusSeeOther)
	}

	// Verify device pending update URL
	var updatedDevice data.Device
	if err := s.DB.First(&updatedDevice, "id = ?", "testdevice").Error; err != nil {
		t.Fatalf("Failed to reload device: %v", err)
	}

	if updatedDevice.PendingUpdateURL == "" {
		t.Error("PendingUpdateURL should be set")
	}

	if !strings.Contains(updatedDevice.PendingUpdateURL, "/static/firmware/custom_testdevice_") {
		t.Errorf("PendingUpdateURL does not look right: %s", updatedDevice.PendingUpdateURL)
	}

	// Verify file existence
	filename := filepath.Base(updatedDevice.PendingUpdateURL)
	filePath := filepath.Join(s.DataDir, "firmware", filename)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Errorf("Uploaded file does not exist at %s", filePath)
	}
	t.Cleanup(func() {
		if err := os.Remove(filePath); err != nil {
			t.Logf("Failed to clean up test file %s: %v", filePath, err)
		}
	})
}

// failingTransport fails every request, standing in for an https address the
// server can't verify or reach.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("certificate signed by unknown authority")
}

func TestOTABaseURL(t *testing.T) {
	s := newTestServerAPI(t)
	s.otaCheckClient = &http.Client{Transport: failingTransport{}}
	reported := func(u string) data.DeviceInfo { return data.DeviceInfo{ImageURL: &u} }
	browser := httptest.NewRequest(http.MethodPost, "https://tronbyt.example.test/devices/x/ota", nil)

	tests := []struct {
		name   string
		device data.Device
		want   string
	}{
		{
			name:   "address the device reports using",
			device: data.Device{Info: reported("ws://192.168.1.10:8000/x/ws"), ImgURL: "https://tronbyt.example.test/x/next"},
			want:   "http://192.168.1.10:8000",
		},
		{
			name:   "configured URL when the device hasn't reported one",
			device: data.Device{ImgURL: "http://192.168.1.10:8000/x/next"},
			want:   "http://192.168.1.10:8000",
		},
		{
			name:   "configured URL when the reported one is unusable",
			device: data.Device{Info: reported("not a url"), ImgURL: "http://192.168.1.10:8000/x/next"},
			want:   "http://192.168.1.10:8000",
		},
		{
			name:   "device's https address without checking the browser's",
			device: data.Device{Info: reported("wss://tronbyt.example.org/x/ws")},
			want:   "https://tronbyt.example.org",
		},
		{
			name:   "browser address as a last resort",
			device: data.Device{},
			want:   "https://tronbyt.example.test",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.otaBaseURL(browser, &tt.device, "/static/firmware/fw.bin"); got != tt.want {
				t.Errorf("otaBaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

// When the device uses plain http but the browser is on https with a trusted
// certificate, the firmware link uses https.
func TestOTABaseURLPrefersTrustedHTTPS(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/static/firmware/fw.bin":
			w.WriteHeader(http.StatusOK)
		case "/static/firmware/moved.bin":
			http.Redirect(w, r, "/elsewhere", http.StatusFound)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)

	s := newTestServerAPI(t)
	s.otaCheckClient = ts.Client() // trusts the test server's certificate
	device := data.Device{ImgURL: "http://192.168.1.10:8000/x/next"}
	browser := httptest.NewRequest(http.MethodPost, ts.URL+"/devices/x/ota", nil)

	if got := s.otaBaseURL(browser, &device, "/static/firmware/fw.bin"); got != ts.URL {
		t.Errorf("trusted https: otaBaseURL() = %q, want %q", got, ts.URL)
	}
	if got := s.otaBaseURL(browser, &device, "/static/firmware/missing.bin"); got != "http://192.168.1.10:8000" {
		t.Errorf("file not found over https: otaBaseURL() = %q, want the device's address", got)
	}
	if got := s.otaBaseURL(browser, &device, "/static/firmware/moved.bin"); got != "http://192.168.1.10:8000" {
		t.Errorf("redirect over https: otaBaseURL() = %q, want the device's address", got)
	}

	s.otaCheckClient = &http.Client{Transport: failingTransport{}}
	if got := s.otaBaseURL(browser, &device, "/static/firmware/fw.bin"); got != "http://192.168.1.10:8000" {
		t.Errorf("untrusted https: otaBaseURL() = %q, want the device's address", got)
	}
}

// A firmware update queued from a browser on localhost must point the device
// at the address it uses, not at localhost.
func TestHandleTriggerOTAUsesDeviceAddress(t *testing.T) {
	s := newTestServerAPI(t)
	var device data.Device
	if err := s.DB.First(&device, "id = ?", "testdevice").Error; err != nil {
		t.Fatalf("Failed to find device: %v", err)
	}
	var user data.User
	if err := s.DB.First(&user, "username = ?", "testuser").Error; err != nil {
		t.Fatalf("Failed to find user 'testuser': %v", err)
	}
	deviceURL := "ws://192.168.1.10:8000/testdevice/ws"
	device.Info.ImageURL = &deviceURL

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("firmware_file", "custom.bin")
	if err != nil {
		t.Fatalf("Failed to create form file: %v", err)
	}
	if _, err := part.Write([]byte("custom firmware content")); err != nil {
		t.Fatalf("Failed to write file content: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Failed to close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://localhost:8000/devices/testdevice/ota", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	s.handleTriggerOTA(httptest.NewRecorder(), req)

	var updated data.Device
	if err := s.DB.First(&updated, "id = ?", "testdevice").Error; err != nil {
		t.Fatalf("Failed to reload device: %v", err)
	}
	if !strings.HasPrefix(updated.PendingUpdateURL, "http://192.168.1.10:8000/static/firmware/custom_testdevice_") {
		t.Errorf("PendingUpdateURL = %q, want it on the device's address", updated.PendingUpdateURL)
	}
	filePath := filepath.Join(s.DataDir, "firmware", filepath.Base(updated.PendingUpdateURL))
	t.Cleanup(func() { _ = os.Remove(filePath) })
}
