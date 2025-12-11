package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestServerAPI(t *testing.T) *Server {
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	if err := db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.WebAuthnCredential{}, &data.Setting{}); err != nil {
		t.Fatalf("Failed to migrate DB: %v", err)
	}

	// Pre-seed settings to avoid "record not found" logs during NewServer
	db.Create(&data.Setting{Key: "secret_key", Value: "testsecret"})
	db.Create(&data.Setting{Key: "system_apps_repo", Value: ""})

	cfg := &config.Settings{}

	s := NewServer(db, cfg)
	s.DataDir = t.TempDir()

	// Setup common test user and device
	adminUser := data.User{
		Username: "admin",
		Password: "$2a$10$w3bQ0wWwWwWwWwWwWwWwWu.D/ZJ.p.Xg.3Q.Q.Q.Q.Q.Q.Q.Q", // Placeholder for hashed password
		APIKey:   "admin_test_api_key",
	}
	if err := db.Create(&adminUser).Error; err != nil {
		t.Fatalf("Failed to create admin user: %v", err)
	}

	user := data.User{
		Username: "testuser",
		Password: "$2a$10$w3bQ0wWwWwWwWwWwWwWwWu.D/ZJ.p.Xg.3Q.Q.Q.Q.Q.Q.Q.Q", // Placeholder for hashed password
		APIKey:   "test_api_key",
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("Failed to create test user: %v", err)
	}

	device := data.Device{
		ID:       "testdevice",
		Username: "testuser",
		Name:     "Test Device",
		APIKey:   "device_api_key",
	}
	if err := db.Create(&device).Error; err != nil {
		t.Fatalf("Failed to create test device: %v", err)
	}

	return s
}

// Helper to create a request with API key.
func newAPIRequest(method, path, apiKey string, body []byte) *http.Request {
	req := httptest.NewRequest(method, path, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	return req
}

func TestHandleDots(t *testing.T) {
	s := newTestServerAPI(t)

	req, _ := http.NewRequest(http.MethodGet, "/dots?w=2&h=1&r=0.75", nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}

	if rr.Header().Get("Content-Type") != "image/svg+xml" {
		t.Errorf("handler returned wrong content type: got %v want %v",
			rr.Header().Get("Content-Type"), "image/svg+xml")
	}

	expectedSVG := `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" width="2" height="1" fill="#fff">
<circle cx="0.500000" cy="0.500000" r="0.750000"/><circle cx="1.500000" cy="0.500000" r="0.750000"/></svg>
`

	if rr.Body.String() != expectedSVG {
		t.Errorf("handler returned unexpected body: got %v want %v",
			rr.Body.String(), expectedSVG)
	}
}

func TestHandleListDevices(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"

	req := newAPIRequest("GET", "/v0/devices", apiKey, nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}

	var payload ListDevicesPayload
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(payload.Devices) != 1 {
		t.Errorf("Expected 1 device, got %d", len(payload.Devices))
	}

	if payload.Devices[0].ID != "testdevice" {
		t.Errorf("Expected device ID 'testdevice', got %s", payload.Devices[0].ID)
	}
}

func TestHandleGetDevice(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"

	req := newAPIRequest("GET", fmt.Sprintf("/v0/devices/%s", deviceID), apiKey, nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}

	var payload DevicePayload
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if payload.ID != deviceID {
		t.Errorf("Expected device ID %s, got %s", deviceID, payload.ID)
	}
}

func TestHandlePushImage(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"
	installID := "testapp"

	// Create a dummy WebP image (a very small, valid WebP header + some data)
	dummyWebp := "UklGRkXlAAAgAAAAAQABAAHAIwAA//VucG/v/4/f//x8oAA=" // Base64 encoded 1x1 green webp

	pushData := PushData{
		InstallationID: installID,
		Image:          dummyWebp,
	}
	body, _ := json.Marshal(pushData)

	req := newAPIRequest("POST", fmt.Sprintf("/v0/devices/%s/push", deviceID), apiKey, body)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}

	// Verify the app was created and image saved
	var app data.App
	if err := s.DB.Where("device_id = ? AND iname = ?", deviceID, installID).First(&app).Error; err != nil {
		t.Fatalf("Expected app to be created, but got error: %v", err)
	}

	if !app.Pushed {
		t.Error("Expected app to be marked as pushed")
	}

	// Verify image file exists
	expectedPath := filepath.Join(s.DataDir, "webp", deviceID, "pushed", installID+".webp")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("Expected pushed image to exist at %s, but it didn't", expectedPath)
	}
}

func TestHandlePushApp(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"
	appID := "testsystemapp"

	// Create dummy system app
	appDir := filepath.Join(s.DataDir, "system-apps", "apps", appID)
	if err := os.MkdirAll(appDir, 0755); err != nil {
		t.Fatalf("Failed to create app dir: %v", err)
	}

	starContent := `
load("render.star", "render")
def main(config):
    return render.Root(child=render.Box(width=64, height=32, color="#00ff00"))
`
	if err := os.WriteFile(filepath.Join(appDir, appID+".star"), []byte(starContent), 0644); err != nil {
		t.Fatalf("Failed to write star file: %v", err)
	}

	// Refresh cache to pick up new app
	s.RefreshSystemAppsCache()

	// Prepare Request
	pushAppData := PushAppData{
		AppID:          appID,
		Config:         map[string]any{"foo": "bar"},
		InstallationID: "testinstall",
	}
	body, _ := json.Marshal(pushAppData)

	req := newAPIRequest("POST", fmt.Sprintf("/v0/devices/%s/push_app", deviceID), apiKey, body)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v: %s",
			rr.Code, http.StatusOK, rr.Body.String())
	}

	if rr.Body.String() != "App pushed." {
		t.Errorf("Expected body 'App pushed.', got '%s'", rr.Body.String())
	}

	// Verify image exists
	expectedPath := filepath.Join(s.DataDir, "webp", deviceID, "pushed", "testinstall.webp")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("Expected pushed image to exist at %s, but it didn't", expectedPath)
	}
}

func TestHandleListInstallations(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"

	// Add a dummy app to the device
	dummyApp := data.App{
		DeviceID:    deviceID,
		Iname:       "dummyapp",
		Name:        "Dummy App",
		UInterval:   10,
		DisplayTime: 10,
		Enabled:     true,
		Order:       0,
	}
	if err := s.DB.Create(&dummyApp).Error; err != nil {
		t.Fatalf("Failed to create dummy app: %v", err)
	}

	req := newAPIRequest("GET", fmt.Sprintf("/v0/devices/%s/installations", deviceID), apiKey, nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}

	var response struct {
		Installations []data.App `json:"installations"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(response.Installations) != 1 || response.Installations[0].Iname != "dummyapp" {
		t.Errorf("Expected 1 installation with iname 'dummyapp', got %v", response.Installations)
	}
}

func TestHandlePatchDevice(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"

	// Initial device state
	var device data.Device
	s.DB.First(&device, "id = ?", deviceID)
	originalBrightness := device.Brightness

	// Patch brightness
	newBrightness := 50
	update := DeviceUpdate{Brightness: &newBrightness}
	body, _ := json.Marshal(update)
	req := newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s", deviceID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v: %s",
			rr.Code, http.StatusOK, rr.Body.String())
	}

	// Verify updated device state
	s.DB.First(&device, "id = ?", deviceID)
	if device.Brightness != data.Brightness(newBrightness) {
		t.Errorf("Expected brightness %d, got %d", newBrightness, device.Brightness)
	}

	// Patch interval
	newInterval := 30
	update = DeviceUpdate{IntervalSec: &newInterval}
	body, _ = json.Marshal(update)
	req = newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s", deviceID), apiKey, body)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v: %s",
			rr.Code, http.StatusOK, rr.Body.String())
	}

	// Verify updated device state
	s.DB.First(&device, "id = ?", deviceID)
	if device.DefaultInterval != newInterval {
		t.Errorf("Expected interval %d, got %d", newInterval, device.DefaultInterval)
	}

	// Restore original brightness to avoid affecting other tests that might rely on it
	update = DeviceUpdate{Brightness: (*int)(&originalBrightness)}
	body, _ = json.Marshal(update)
	req = newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s", deviceID), apiKey, body)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)
}

func TestHandlePatchInstallation(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"
	installID := "patchapp"

	// Add a dummy app to the device
	app := data.App{
		DeviceID:    deviceID,
		Iname:       installID,
		Name:        "Patch App",
		UInterval:   10,
		DisplayTime: 10,
		Enabled:     true,
		Order:       0,
	}
	if err := s.DB.Create(&app).Error; err != nil {
		t.Fatalf("Failed to create dummy app: %v", err)
	}

	// Patch enabled status
	newEnabled := false
	update := InstallationUpdate{Enabled: &newEnabled}
	body, _ := json.Marshal(update)
	req := newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s/installations/%s", deviceID, installID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v: %s",
			rr.Code, http.StatusOK, rr.Body.String())
	}

	// Verify updated app state
	s.DB.First(&app, "iname = ?", installID)
	if app.Enabled != newEnabled {
		t.Errorf("Expected app enabled to be %t, got %t", newEnabled, app.Enabled)
	}
}

func TestHandleDeleteInstallationAPI(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"
	installID := "deleteapp"

	// Add a dummy app to the device
	app := data.App{
		DeviceID:    deviceID,
		Iname:       installID,
		Name:        "Delete App",
		UInterval:   10,
		DisplayTime: 10,
		Enabled:     true,
		Order:       0,
		Path:        nil, // Important for cleanup not to try to delete a non-existent file
	}
	if err := s.DB.Create(&app).Error; err != nil {
		t.Fatalf("Failed to create dummy app: %v", err)
	}

	req := newAPIRequest("DELETE", fmt.Sprintf("/v0/devices/%s/installations/%s", deviceID, installID), apiKey, nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}

	// Verify app is deleted
	var deletedApp data.App
	if err := s.DB.Where("device_id = ? AND iname = ?", deviceID, installID).First(&deletedApp).Error; err == nil {
		t.Errorf("App was not deleted")
	}
}
