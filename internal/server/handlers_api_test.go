package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestServerAPI(t *testing.T) *Server {
	// Use a private in-memory database (`cache=private`) to ensure each test gets a completely
	// isolated database. We must also limit the connection pool to a single connection
	// (`SetMaxOpenConns(1)`) because each new connection to a private in-memory database
	// would create a new, separate database. This configuration is crucial to prevent both
	// race conditions (like "database table is locked") and data visibility issues across
	// different operations within the same test.
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=private"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("Failed to get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Logf("Failed to close DB: %v", err)
		}
	})

	if err := db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.WebAuthnCredential{}, &data.Setting{}); err != nil {
		t.Fatalf("Failed to migrate DB: %v", err)
	}

	ctx := context.Background()

	// Pre-seed settings to avoid "record not found" logs during NewServer
	if err := gorm.G[data.Setting](db).Create(ctx, &data.Setting{Key: "secret_key", Value: "testsecret"}); err != nil {
		t.Fatalf("Failed to seed secret_key: %v", err)
	}
	if err := gorm.G[data.Setting](db).Create(ctx, &data.Setting{Key: "system_apps_repo", Value: ""}); err != nil {
		t.Fatalf("Failed to seed system_apps_repo: %v", err)
	}

	cfg := &config.Settings{}

	s := NewServer(db, cfg)
	s.DataDir = t.TempDir()

	// Setup common test user and device
	adminUser := data.User{
		Username: "admin",
		Password: "$2a$10$w3bQ0wWwWwWwWwWwWwWwWu.D/ZJ.p.Xg.3Q.Q.Q.Q.Q.Q.Q.Q", // Placeholder for hashed password
		Email:    stringPtr("admin@example.com"),
		APIKey:   "admin_test_api_key",
	}
	if err := gorm.G[data.User](db).Create(ctx, &adminUser); err != nil {
		t.Fatalf("Failed to create admin user: %v", err)
	}

	user := data.User{
		Username: "testuser",
		Password: "$2a$10$w3bQ0wWwWwWwWwWwWwWwWu.D/ZJ.p.Xg.3Q.Q.Q.Q.Q.Q.Q.Q", // Placeholder for hashed password
		Email:    stringPtr("test@example.com"),
		APIKey:   "test_api_key",
	}
	if err := gorm.G[data.User](db).Create(ctx, &user); err != nil {
		t.Fatalf("Failed to create test user: %v", err)
	}

	device := data.Device{
		ID:       "testdevice",
		Username: "testuser",
		Name:     "Test Device",
		Type:     data.DeviceTidbytGen1,
		APIKey:   "device_api_key",
	}
	if err := gorm.G[data.Device](db).Create(ctx, &device); err != nil {
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

	expectedSVG := `<svg xmlns="http://www.w3.org/2000/svg" width="2" height="1" fill="#fff"><defs><pattern id="dot" width="1" height="1" patternUnits="userSpaceOnUse"><circle cx=".5" cy=".5" r=".75"/></pattern></defs><rect width="100%" height="100%" fill="url(#dot)"/></svg>`

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

	// Update device with new Info fields
	device, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	if err != nil {
		t.Fatalf("Failed to fetch device: %v", err)
	}

	device.Info.SSID = stringPtr("Test SSID")
	device.Info.WifiPowerSave = intPtr(1)
	device.Info.SkipDisplayVersion = boolPtr(true)
	device.Info.APMode = boolPtr(true)
	device.Info.PreferIPv6 = boolPtr(true)
	device.Info.SwapColors = boolPtr(true)
	device.Info.ImageURL = stringPtr("http://example.com/image.png")
	if err := s.DB.Save(device).Error; err != nil {
		t.Fatalf("Failed to update device info: %v", err)
	}

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
	// Verify new fields
	if payload.Info.SSID == nil || *payload.Info.SSID != "Test SSID" {
		t.Errorf("Expected SSID 'Test SSID', got '%v'", payload.Info.SSID)
	}
	if payload.Info.WifiPowerSave == nil || *payload.Info.WifiPowerSave != 1 {
		t.Errorf("Expected WifiPowerSave 1, got %v", payload.Info.WifiPowerSave)
	}
	if payload.Info.SkipDisplayVersion == nil || !*payload.Info.SkipDisplayVersion {
		t.Errorf("Expected SkipDisplayVersion to be true, got %v", payload.Info.SkipDisplayVersion)
	}
	if payload.Info.APMode == nil || !*payload.Info.APMode {
		t.Errorf("Expected APMode to be true, got %v", payload.Info.APMode)
	}
	if payload.Info.PreferIPv6 == nil || !*payload.Info.PreferIPv6 {
		t.Errorf("Expected PreferIPv6 to be true, got %v", payload.Info.PreferIPv6)
	}
	if payload.Info.SwapColors == nil || !*payload.Info.SwapColors {
		t.Errorf("Expected SwapColors to be true, got %v", payload.Info.SwapColors)
	}
	if payload.Info.ImageURL == nil || *payload.Info.ImageURL != "http://example.com/image.png" {
		t.Errorf("Expected ImageURL 'http://example.com/image.png', got '%v'", payload.Info.ImageURL)
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
	app, err := gorm.G[data.App](s.DB).Where("device_id = ? AND iname = ?", deviceID, installID).First(context.Background())
	if err != nil {
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
	if err := gorm.G[data.App](s.DB).Create(context.Background(), &dummyApp); err != nil {
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
		Installations []AppPayload `json:"installations"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(response.Installations) != 1 || response.Installations[0].ID != "dummyapp" {
		t.Errorf("Expected 1 installation with ID 'dummyapp', got %v", response.Installations)
	}
}

func TestHandleGetInstallation(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"
	installID := "dummyapp"

	// Add a dummy app to the device
	dummyApp := data.App{
		DeviceID:    deviceID,
		Iname:       installID,
		Name:        "Dummy App",
		UInterval:   10,
		DisplayTime: 10,
		Enabled:     true,
		Order:       0,
	}
	if err := gorm.G[data.App](s.DB).Create(context.Background(), &dummyApp); err != nil {
		t.Fatalf("Failed to create dummy app: %v", err)
	}

	req := newAPIRequest("GET", fmt.Sprintf("/v0/devices/%s/installations/%s", deviceID, installID), apiKey, nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}

	var payload AppPayload
	if err := json.NewDecoder(rr.Body).Decode(&payload); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if payload.ID != installID {
		t.Errorf("Expected app ID %s, got %s", installID, payload.ID)
	}
	if payload.AppID != "Dummy App" {
		t.Errorf("Expected app name 'Dummy App', got %s", payload.AppID)
	}
}

func TestHandlePatchDevice(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"

	// Initial device state
	device, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	if err != nil {
		t.Fatalf("Failed to fetch initial device state: %v", err)
	}
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
	device, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	if err != nil {
		t.Fatalf("Failed to fetch updated device state: %v", err)
	}
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
	device, err = gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	if err != nil {
		t.Fatalf("Failed to fetch updated device state: %v", err)
	}
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
	if err := gorm.G[data.App](s.DB).Create(context.Background(), &app); err != nil {
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
	app, err := gorm.G[data.App](s.DB).Where("iname = ?", installID).First(context.Background())
	if err != nil {
		t.Fatalf("Failed to fetch updated app state: %v", err)
	}
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
	if err := gorm.G[data.App](s.DB).Create(context.Background(), &app); err != nil {
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
	if _, err := gorm.G[data.App](s.DB).Where("device_id = ? AND iname = ?", deviceID, installID).First(context.Background()); err == nil {
		t.Errorf("App was not deleted")
	}
}

func TestHandlePatchDeviceDeviceKey(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"
	installID := "testapp"

	// Create a dummy app for the device
	dummyApp := data.App{
		DeviceID:    deviceID,
		Iname:       installID,
		Name:        "Test App",
		UInterval:   10,
		DisplayTime: 10,
		Enabled:     true,
		Order:       0,
	}
	if err := gorm.G[data.App](s.DB).Create(context.Background(), &dummyApp); err != nil {
		t.Fatalf("Failed to create dummy app: %v", err)
	}

	// Patch pinnedApp using device key
	update := DeviceUpdate{PinnedApp: &installID}
	body, _ := json.Marshal(update)
	req := newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s", deviceID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v: %s",
			rr.Code, http.StatusOK, rr.Body.String())
	}

	// Verify updated device state
	device, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	if err != nil {
		t.Fatalf("Failed to fetch device: %v", err)
	}
	if device.PinnedApp == nil || *device.PinnedApp != installID {
		t.Errorf("Expected pinnedApp %s, got %v", installID, device.PinnedApp)
	}
}

func TestHandleListInstallationsDeviceKey(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
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
	if err := gorm.G[data.App](s.DB).Create(context.Background(), &dummyApp); err != nil {
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
		Installations []AppPayload `json:"installations"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if len(response.Installations) != 1 {
		t.Errorf("Expected 1 installation, got %d", len(response.Installations))
	}
}

func TestHandleUpdateFirmwareSettingsAPI(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"

	// Subscribe to broadcaster to check for notification
	ch := s.Broadcaster.Subscribe(deviceID)
	defer s.Broadcaster.Unsubscribe(deviceID, ch)

	// Construct JSON request body
	payload := FirmwareSettingsUpdate{
		SkipDisplayVersion: boolPtr(true),
		WifiPowerSave:      intPtr(2),
		ImageURL:           stringPtr("http://example.com/test.png"),
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/v0/devices/%s/update_firmware_settings", deviceID), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v: %s",
			rr.Code, http.StatusOK, rr.Body.String())
	}

	if rr.Body.String() != "Firmware settings updated." {
		t.Errorf("Expected body 'Firmware settings updated.', got '%s'", rr.Body.String())
	}

	// Verify broadcaster notification
	select {
	case msg := <-ch:
		cmdMsg, ok := msg.(DeviceCommandMessage)
		if !ok {
			t.Fatalf("unexpected message type from broadcaster: %T", msg)
		}

		var receivedPayload map[string]any
		if err := json.Unmarshal(cmdMsg.Payload, &receivedPayload); err != nil {
			t.Fatalf("failed to unmarshal payload from broadcaster: %v", err)
		}

		if len(receivedPayload) != 3 {
			t.Errorf("expected 3 fields in payload, got %d", len(receivedPayload))
		}
		if val, ok := receivedPayload["skip_display_version"].(bool); !ok || !val {
			t.Errorf("expected skip_display_version to be true, got %v", receivedPayload["skip_display_version"])
		}
		if val, ok := receivedPayload["wifi_power_save"].(float64); !ok || val != 2 {
			t.Errorf("expected wifi_power_save to be 2, got %v", receivedPayload["wifi_power_save"])
		}
		if val, ok := receivedPayload["image_url"].(string); !ok || val != "http://example.com/test.png" {
			t.Errorf("expected image_url to be 'http://example.com/test.png', got '%v'", receivedPayload["image_url"])
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for broadcaster notification")
	}
}

func TestHandleRebootDeviceAPI(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"

	req := newAPIRequest("POST", fmt.Sprintf("/v0/devices/%s/reboot", deviceID), apiKey, nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v: %s",
			rr.Code, http.StatusOK, rr.Body.String())
	}

	if rr.Body.String() != "Reboot command sent." {
		t.Errorf("Expected body 'Reboot command sent.', got '%s'", rr.Body.String())
	}
}
