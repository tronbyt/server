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
	"strings"
	"testing"
	"time"

	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	cfg := &config.Settings{
		DataDir:            t.TempDir(),
		Production:         false,
		EnableUpdateChecks: false,
	}

	s := NewServer(db, cfg)

	// Setup common test user and device
	adminUser := data.User{
		Username: "admin",
		Password: "$2a$10$w3bQ0wWwWwWwWwWwWwWwWu.D/ZJ.p.Xg.3Q.Q.Q.Q.Q.Q.Q.Q", // Placeholder for hashed password
		Email:    new("admin@example.com"),
		APIKey:   "admin_test_api_key",
		IsAdmin:  true,
	}
	if err := gorm.G[data.User](db).Create(ctx, &adminUser); err != nil {
		t.Fatalf("Failed to create admin user: %v", err)
	}

	user := data.User{
		Username: "testuser",
		Password: "$2a$10$w3bQ0wWwWwWwWwWwWwWwWu.D/ZJ.p.Xg.3Q.Q.Q.Q.Q.Q.Q.Q", // Placeholder for hashed password
		Email:    new("test@example.com"),
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

	device.Info.SSID = new("Test SSID")
	device.Info.WifiPowerSave = new(1)
	device.Info.SkipDisplayVersion = new(true)
	device.Info.SkipBootAnimation = new(true)
	device.Info.APMode = new(true)
	device.Info.PreferIPv6 = new(true)
	device.Info.SwapColors = new(true)
	device.Info.ImageURL = new("http://example.com/image.png")
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
	if payload.Info.SkipBootAnimation == nil || !*payload.Info.SkipBootAnimation {
		t.Errorf("Expected SkipBootAnimation to be true, got %v", payload.Info.SkipBootAnimation)
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
	// New pushed apps get a numeric iname, so check by path containing installID
	app, err := gorm.G[data.App](s.DB).Where("device_id = ? AND path = ?", deviceID, "pushed:"+installID).First(context.Background())
	if err != nil {
		t.Fatalf("Expected app to be created, but got error: %v", err)
	}

	if !app.Pushed {
		t.Error("Expected app to be marked as pushed")
	}

	// Verify the iname is numeric (the new behavior for pushed apps)
	if app.Iname == "" || app.Iname[0] < '0' || app.Iname[0] > '9' {
		t.Errorf("Expected numeric iname for pushed app, got %s", app.Iname)
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

	// Verify image exists (installID-based: testinstall.webp)
	expectedPath := filepath.Join(s.DataDir, "webp", deviceID, "pushed", "testinstall.webp")
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Errorf("Expected pushed image to exist at %s, but it didn't", expectedPath)
	}
}

func TestHandlePushAppUpdatesExistingInstallation(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"
	appID := "colorapp"

	appDir := filepath.Join(s.DataDir, "system-apps", "apps", appID)
	require.NoError(t, os.MkdirAll(appDir, 0755))

	starContent := `
load("render.star", "render")
def main(config):
    color = config.get("color", "#ffffff")
    return render.Root(child=render.Box(width=64, height=32, color=color))
`
	require.NoError(t, os.WriteFile(filepath.Join(appDir, appID+".star"), []byte(starContent), 0644))
	s.RefreshSystemAppsCache()

	const installID = "100"
	imagePath := filepath.Join(s.DataDir, "webp", deviceID, "pushed", installID+".webp")

	doPush := func(color string) {
		body, _ := json.Marshal(PushAppData{
			AppID:          appID,
			Config:         map[string]any{"color": color},
			InstallationID: installID,
		})
		req := newAPIRequest("POST", fmt.Sprintf("/v0/devices/%s/push_app", deviceID), apiKey, body)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	}

	doPush("#ff0000")
	imgRed, err := os.ReadFile(imagePath)
	require.NoError(t, err)

	doPush("#0000ff")
	imgBlue, err := os.ReadFile(imagePath)
	require.NoError(t, err)

	// Re-pushing with a different color config must produce a different image.
	assert.NotEqual(t, imgRed, imgBlue,
		"re-pushing to an existing installationID with new config should update the saved image")

	// Exactly one pushed installation should exist.
	count, err := gorm.G[data.App](s.DB).Where("device_id = ? AND pushed = ?", deviceID, true).Count(context.Background(), "*")
	require.NoError(t, err)
	assert.Equal(t, int64(1), count,
		"pushing to an existing installationID should not create a second installation")
}

// TestHandlePushAppMissingCachedImage verifies that when the cached webp is missing,
// the handler falls through to the render path rather than erroring. A push that
// provides an appID and config should succeed and produce a new image.
func TestHandlePushAppMissingCachedImage(t *testing.T) {
	s := newTestServerAPI(t)
	ctx := context.Background()
	apiKey := "device_api_key"
	deviceID := "testdevice"
	appID := "colorapp"
	installID := "orphaned"
	installPath := "pushed:" + installID

	appDir := filepath.Join(s.DataDir, "system-apps", "apps", appID)
	require.NoError(t, os.MkdirAll(appDir, 0755))
	starContent := `
load("render.star", "render")
def main(config):
    color = config.get("color", "#ffffff")
    return render.Root(child=render.Box(width=64, height=32, color=color))
`
	require.NoError(t, os.WriteFile(filepath.Join(appDir, appID+".star"), []byte(starContent), 0644))
	s.RefreshSystemAppsCache()

	// Seed a pushed app record without creating the corresponding webp file.
	pushedApp := data.App{
		DeviceID:    deviceID,
		Iname:       "100",
		Name:        "pushed",
		UInterval:   10,
		DisplayTime: 0,
		Enabled:     true,
		Pushed:      true,
		Path:        &installPath,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &pushedApp))

	// Providing appID + config should fall through to a fresh render and succeed.
	body, _ := json.Marshal(PushAppData{
		InstallationID: installID,
		AppID:          appID,
		Config:         map[string]any{"color": "#ff0000"},
	})
	req := newAPIRequest("POST", fmt.Sprintf("/v0/devices/%s/push_app", deviceID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	imagePath := filepath.Join(s.DataDir, "webp", deviceID, "pushed", installID+".webp")
	_, err := os.Stat(imagePath)
	assert.NoError(t, err, "re-render should have created the missing cached image")
}

// seedPushedInstallation creates a pushed app DB record and writes sentinel bytes as
// the cached webp so tests can detect whether the file was replaced or left intact.
func seedPushedInstallation(t *testing.T, s *Server, deviceID, installID string) []byte {
	t.Helper()
	ctx := context.Background()
	installPath := "pushed:" + installID
	app := data.App{
		DeviceID:    deviceID,
		Iname:       "200",
		Name:        "pushed",
		UInterval:   10,
		DisplayTime: 0,
		Enabled:     true,
		Pushed:      true,
		Path:        &installPath,
	}
	require.NoError(t, gorm.G[data.App](s.DB).Create(ctx, &app))

	pushedDir := filepath.Join(s.DataDir, "webp", deviceID, "pushed")
	require.NoError(t, os.MkdirAll(pushedDir, 0755))
	sentinel := []byte("sentinel-cached-image")
	require.NoError(t, os.WriteFile(filepath.Join(pushedDir, installID+".webp"), sentinel, 0644))
	return sentinel
}

func setupColorApp(t *testing.T, s *Server) string {
	t.Helper()
	appID := "colorapp"
	appDir := filepath.Join(s.DataDir, "system-apps", "apps", appID)
	require.NoError(t, os.MkdirAll(appDir, 0755))
	star := `
load("render.star", "render")
def main(config):
    color = config.get("color", "#ffffff")
    return render.Root(child=render.Box(width=64, height=32, color=color))
`
	require.NoError(t, os.WriteFile(filepath.Join(appDir, appID+".star"), []byte(star), 0644))
	s.RefreshSystemAppsCache()
	return appID
}

// TestHandlePushAppConfigReplacesCache verifies that providing a config always
// triggers a fresh render, replacing the cached image rather than serving it.
func TestHandlePushAppConfigReplacesCache(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"
	appID := setupColorApp(t, s)
	installID := "cached-install"
	sentinel := seedPushedInstallation(t, s, deviceID, installID)

	body, _ := json.Marshal(PushAppData{
		InstallationID: installID,
		AppID:          appID,
		Config:         map[string]any{"color": "#ff0000"},
	})
	req := newAPIRequest("POST", fmt.Sprintf("/v0/devices/%s/push_app", deviceID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	result, err := os.ReadFile(filepath.Join(s.DataDir, "webp", deviceID, "pushed", installID+".webp"))
	require.NoError(t, err)
	assert.NotEqual(t, sentinel, result,
		"providing config must trigger a fresh render, not serve the cached image")
}

// TestHandlePushAppNoCacheConfigServesCache verifies that omitting config and app_id
// serves the existing cached image without re-rendering.
func TestHandlePushAppNoCacheConfigServesCache(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"
	installID := "cached-install"
	sentinel := seedPushedInstallation(t, s, deviceID, installID)

	body, _ := json.Marshal(PushAppData{InstallationID: installID})
	req := newAPIRequest("POST", fmt.Sprintf("/v0/devices/%s/push_app", deviceID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	result, err := os.ReadFile(filepath.Join(s.DataDir, "webp", deviceID, "pushed", installID+".webp"))
	require.NoError(t, err)
	assert.Equal(t, sentinel, result,
		"omitting config and app_id must serve the cached image without re-rendering")
}

func TestHandlePushAppAppIDOnly(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"
	appID := setupColorApp(t, s)

	body, _ := json.Marshal(PushAppData{
		AppID:  appID,
		Config: map[string]any{"color": "#ff0000"},
	})
	req := newAPIRequest("POST", fmt.Sprintf("/v0/devices/%s/push_app", deviceID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	count, err := gorm.G[data.App](s.DB).Where("device_id = ? AND pushed = ?", deviceID, true).Count(context.Background(), "*")
	require.NoError(t, err)
	assert.Equal(t, int64(0), count, "push with no installationID must not create an installation record")
}

func TestHandlePushAppNoAppIDNoInstallationID(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"

	body, _ := json.Marshal(PushAppData{})
	req := newAPIRequest("POST", fmt.Sprintf("/v0/devices/%s/push_app", deviceID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlePushAppBackground(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "device_api_key"
	deviceID := "testdevice"
	appID := setupColorApp(t, s)
	installID := "bg-install"

	ch := s.Broadcaster.Subscribe(deviceID)
	defer s.Broadcaster.Unsubscribe(deviceID, ch)

	body, _ := json.Marshal(PushAppData{
		AppID:          appID,
		InstallationID: installID,
		Config:         map[string]any{"color": "#ff0000"},
		Background:     true,
	})
	req := newAPIRequest("POST", fmt.Sprintf("/v0/devices/%s/push_app", deviceID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Image must be saved.
	imagePath := filepath.Join(s.DataDir, "webp", deviceID, "pushed", installID+".webp")
	_, err := os.Stat(imagePath)
	assert.NoError(t, err, "background push must save the rendered image")

	// Broadcaster must not have been notified.
	select {
	case <-ch:
		t.Error("background push must not notify the device broadcaster")
	default:
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

func TestHandlePatchDeviceNightModeActive(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"

	device, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)
	device.NightModeEnabled = true
	device.NightStart = "22:00"
	device.NightEnd = "06:00"
	require.NoError(t, s.DB.Save(device).Error)

	active := true
	update := DeviceUpdate{NightModeActive: &active}
	body, _ := json.Marshal(update)
	req := newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s", deviceID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var payload DevicePayload
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&payload))
	assert.True(t, payload.NightMode.Active)
	require.NotNil(t, payload.NightMode.OverrideUntil)

	updatedDevice, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)
	require.NotNil(t, updatedDevice.NightModeOverride)
	require.NotNil(t, updatedDevice.NightModeOverrideUntil)
	assert.True(t, *updatedDevice.NightModeOverride)
}

func TestHandlePatchDeviceDimModeActive(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"

	device, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)
	dimTime := "18:00"
	device.DimModeEnabled = true
	device.DimTime = &dimTime
	require.NoError(t, s.DB.Save(device).Error)

	active := true
	update := DeviceUpdate{DimModeActive: &active}
	body, _ := json.Marshal(update)
	req := newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s", deviceID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	var payload DevicePayload
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&payload))
	assert.True(t, payload.DimMode.Active)
	require.NotNil(t, payload.DimMode.OverrideUntil)

	updatedDevice, err := gorm.G[data.Device](s.DB).Where("id = ?", deviceID).First(context.Background())
	require.NoError(t, err)
	require.NotNil(t, updatedDevice.DimModeOverride)
	require.NotNil(t, updatedDevice.DimModeOverrideUntil)
	assert.True(t, *updatedDevice.DimModeOverride)
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

func TestHandlePatchInstallationSchedule(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"
	installID := "scheduleapp"

	// Add a dummy app to the device
	app := data.App{
		DeviceID:    deviceID,
		Iname:       installID,
		Name:        "Schedule App",
		UInterval:   10,
		DisplayTime: 10,
		Enabled:     true,
		Order:       0,
	}
	if err := gorm.G[data.App](s.DB).Create(context.Background(), &app); err != nil {
		t.Fatalf("Failed to create dummy app: %v", err)
	}

	// Patch schedule fields
	startTime := "09:00"
	endTime := "17:00"
	days := []string{"monday", "wednesday", "friday"}
	useCustomRecurrence := true
	recurrenceType := data.RecurrenceWeekly
	recurrenceInterval := 2
	recurrenceStartDate := "2026-01-01"
	recurrenceEndDate := "2026-12-31"

	update := InstallationUpdate{
		StartTime:           &startTime,
		EndTime:             &endTime,
		Days:                &days,
		UseCustomRecurrence: &useCustomRecurrence,
		RecurrenceType:      &recurrenceType,
		RecurrenceInterval:  &recurrenceInterval,
		RecurrenceStartDate: &recurrenceStartDate,
		RecurrenceEndDate:   &recurrenceEndDate,
	}
	body, _ := json.Marshal(update)
	req := newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s/installations/%s", deviceID, installID), apiKey, body)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v: %s",
			rr.Code, http.StatusOK, rr.Body.String())
	}

	// Verify updated app state from DB
	updated, err := gorm.G[data.App](s.DB).Where("iname = ?", installID).First(context.Background())
	if err != nil {
		t.Fatalf("Failed to fetch updated app state: %v", err)
	}

	if updated.StartTime == nil || *updated.StartTime != "09:00" {
		t.Errorf("Expected startTime '09:00', got %v", updated.StartTime)
	}
	if updated.EndTime == nil || *updated.EndTime != "17:00" {
		t.Errorf("Expected endTime '17:00', got %v", updated.EndTime)
	}
	if len(updated.Days) != 3 || updated.Days[0] != "monday" {
		t.Errorf("Expected days [monday wednesday friday], got %v", updated.Days)
	}
	if !updated.UseCustomRecurrence {
		t.Errorf("Expected useCustomRecurrence true, got false")
	}
	if updated.RecurrenceType != data.RecurrenceWeekly {
		t.Errorf("Expected recurrenceType 'weekly', got %s", updated.RecurrenceType)
	}
	if updated.RecurrenceInterval != 2 {
		t.Errorf("Expected recurrenceInterval 2, got %d", updated.RecurrenceInterval)
	}
	if updated.RecurrenceStartDate == nil || *updated.RecurrenceStartDate != "2026-01-01" {
		t.Errorf("Expected recurrenceStartDate '2026-01-01', got %v", updated.RecurrenceStartDate)
	}
	if updated.RecurrenceEndDate == nil || *updated.RecurrenceEndDate != "2026-12-31" {
		t.Errorf("Expected recurrenceEndDate '2026-12-31', got %v", updated.RecurrenceEndDate)
	}

	// Verify the response JSON also contains the schedule fields
	var respApp data.App
	if err := json.NewDecoder(rr.Body).Decode(&respApp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}
	if respApp.StartTime == nil || *respApp.StartTime != "09:00" {
		t.Errorf("Response startTime: expected '09:00', got %v", respApp.StartTime)
	}

	// Now test clearing schedule fields by sending empty strings
	emptyStr := ""
	clearUpdate := InstallationUpdate{
		StartTime: &emptyStr,
		EndTime:   &emptyStr,
	}
	body, _ = json.Marshal(clearUpdate)
	req = newAPIRequest("PATCH", fmt.Sprintf("/v0/devices/%s/installations/%s", deviceID, installID), apiKey, body)
	rr = httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("clear schedule: got status %v want %v: %s", rr.Code, http.StatusOK, rr.Body.String())
	}

	cleared, err := gorm.G[data.App](s.DB).Where("iname = ?", installID).First(context.Background())
	if err != nil {
		t.Fatalf("Failed to fetch cleared app state: %v", err)
	}
	if cleared.StartTime != nil {
		t.Errorf("Expected startTime nil after clear, got %v", *cleared.StartTime)
	}
	if cleared.EndTime != nil {
		t.Errorf("Expected endTime nil after clear, got %v", *cleared.EndTime)
	}
	// Days should still be set from the previous update
	if len(cleared.Days) != 3 {
		t.Errorf("Expected days to remain unchanged, got %v", cleared.Days)
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

func TestHandleDeleteInstallationAPI_ByInstallationID(t *testing.T) {
	s := newTestServerAPI(t)
	apiKey := "test_api_key"
	deviceID := "testdevice"
	installationID := "my-custom-pushed-app"
	pushedPath := "pushed:" + installationID

	// Add a pushed app with a numeric iname but custom installationID in path
	app := data.App{
		DeviceID:    deviceID,
		Iname:       "999", // Server-generated numeric iname
		Name:        "pushed",
		UInterval:   10,
		DisplayTime: 0,
		Enabled:     true,
		Order:       0,
		Pushed:      true,
		Path:        &pushedPath,
	}
	if err := gorm.G[data.App](s.DB).Create(context.Background(), &app); err != nil {
		t.Fatalf("Failed to create pushed app: %v", err)
	}

	// Delete using the user-supplied installationID (not the numeric iname)
	req := newAPIRequest("DELETE", fmt.Sprintf("/v0/devices/%s/installations/%s", deviceID, installationID), apiKey, nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("handler returned wrong status code: got %v want %v, body: %s",
			rr.Code, http.StatusOK, rr.Body.String())
	}

	// Verify app is deleted
	if _, err := gorm.G[data.App](s.DB).Where("device_id = ? AND iname = ?", deviceID, "999").First(context.Background()); err == nil {
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
		SkipDisplayVersion: new(true),
		SkipBootAnimation:  new(true),
		WifiPowerSave:      new(2),
		ImageURL:           new("http://example.com/test.png"),
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

		if len(receivedPayload) != 4 {
			t.Errorf("expected 4 fields in payload, got %d", len(receivedPayload))
		}
		if val, ok := receivedPayload["skip_display_version"].(bool); !ok || !val {
			t.Errorf("expected skip_display_version to be true, got %v", receivedPayload["skip_display_version"])
		}
		if val, ok := receivedPayload["skip_boot_animation"].(bool); !ok || !val {
			t.Errorf("expected skip_boot_animation to be true, got %v", receivedPayload["skip_boot_animation"])
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

	var updatedDevice data.Device
	if err := s.DB.First(&updatedDevice, "id = ?", deviceID).Error; err != nil {
		t.Fatalf("Failed to fetch device: %v", err)
	}
	if updatedDevice.PendingImageURL != "http://example.com/test.png" {
		t.Errorf("expected pending image URL to be queued, got %q", updatedDevice.PendingImageURL)
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

	var updatedDevice data.Device
	if err := s.DB.First(&updatedDevice, "id = ?", deviceID).Error; err != nil {
		t.Fatalf("Failed to fetch device: %v", err)
	}
	if !updatedDevice.PendingReboot {
		t.Error("expected pending reboot to be queued")
	}
}

func TestSavePushedImage_CoalesceID(t *testing.T) {
	s := newTestServerAPI(t)
	deviceID := "testdevice"

	pushedDir := filepath.Join(s.DataDir, "webp", deviceID, "pushed")

	// Save 5 coalesced pushes with the same coalesceID — only 1 should remain
	for i := range 5 {
		imgData := fmt.Appendf(nil, "coalesced image %d", i)
		if err := s.savePushedImage(deviceID, "", "mycoalesce", imgData); err != nil {
			t.Fatalf("Failed to save coalesced push %d: %v", i, err)
		}
		time.Sleep(1 * time.Millisecond)
	}

	entries, err := os.ReadDir(pushedDir)
	if err != nil {
		t.Fatalf("Failed to read pushed dir: %v", err)
	}

	// With coalesceID, at most 1 file with that ID (__{timestamp}_{coalesceID}.webp)
	coalescedCount := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "__") && strings.HasSuffix(entry.Name(), "_mycoalesce.webp") {
			coalescedCount++
		}
	}
	if coalescedCount != 1 {
		t.Errorf("Expected 1 coalesced file, got %d: %v", coalescedCount, entryNames(pushedDir))
	}
}

func TestSavePushedImage_CoalesceID_SuffixCollision(t *testing.T) {
	s := newTestServerAPI(t)
	deviceID := "testdevice-collision"
	pushedDir := filepath.Join(s.DataDir, "webp", deviceID, "pushed")

	// Save with coalesceID "foo"
	if err := s.savePushedImage(deviceID, "", "foo", []byte("foo")); err != nil {
		t.Fatalf("Failed to save with coalesceID 'foo': %v", err)
	}
	// Save with coalesceID "bar_foo" (must not collide with "foo")
	if err := s.savePushedImage(deviceID, "", "bar_foo", []byte("bar_foo")); err != nil {
		t.Fatalf("Failed to save with coalesceID 'bar_foo': %v", err)
	}

	entries, err := os.ReadDir(pushedDir)
	if err != nil {
		t.Fatalf("Failed to read pushed dir: %v", err)
	}

	// Both files should exist — "bar_foo" must not have deleted "foo"'s file.
	// Extract coalesceID from filenames properly (split at first _ after __).
	fooCount := 0
	barFooCount := 0
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, "__") && strings.HasSuffix(name, ".webp") {
			inner := name[2 : len(name)-5]
			if _, fileCoalesceID, found := strings.Cut(inner, "_"); found {
				switch fileCoalesceID {
				case "foo":
					fooCount++
				case "bar_foo":
					barFooCount++
				}
			}
		}
	}
	if fooCount != 1 {
		t.Errorf("Expected 1 file for coalesceID 'foo', got %d: %v", fooCount, entryNames(pushedDir))
	}
	if barFooCount != 1 {
		t.Errorf("Expected 1 file for coalesceID 'bar_foo', got %d: %v", barFooCount, entryNames(pushedDir))
	}
}

func TestSavePushedImage_AnonymousExpiry(t *testing.T) {
	s := newTestServerAPI(t)
	deviceID := "testdevice-expiry"
	pushedDir := filepath.Join(s.DataDir, "webp", deviceID, "pushed")

	// Create an anonymous file with timestamp from 48 hours ago
	oldNanos := time.Now().UnixNano() - 48*int64(time.Hour)
	oldName := fmt.Sprintf("__%d.webp", oldNanos)
	if err := os.MkdirAll(pushedDir, 0755); err != nil {
		t.Fatalf("failed to create pushed dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pushedDir, oldName), []byte("old"), 0644); err != nil {
		t.Fatalf("failed to write old file: %v", err)
	}

	// Save a new anonymous push — triggers async cleanup of the 48-hour-old file
	if err := s.savePushedImage(deviceID, "", "", []byte("new")); err != nil {
		t.Fatalf("Failed to save anonymous push: %v", err)
	}

	// Cleanup runs in a background goroutine; wait for it.
	assert.Eventually(t, func() bool {
		entries, err := os.ReadDir(pushedDir)
		if err != nil {
			return false
		}
		count := 0
		for _, entry := range entries {
			if isAnonymousEphemeral(entry.Name()) {
				count++
			}
		}
		return count == 1
	}, 2*time.Second, 10*time.Millisecond, "Expected 1 anonymous ephemeral file after async cleanup")
}

func TestSavePushedImage_CoalesceID_Invalid(t *testing.T) {
	s := newTestServerAPI(t)

	// Too long
	longID := strings.Repeat("a", 65)
	if err := s.savePushedImage("testdevice", "", longID, []byte("x")); err == nil {
		t.Error("Expected error for coalesceID > 64 chars, got nil")
	}

	// Invalid characters (slash)
	if err := s.savePushedImage("testdevice", "", "foo/bar", []byte("x")); err == nil {
		t.Error("Expected error for coalesceID with slash, got nil")
	}

	// Invalid characters (dot)
	if err := s.savePushedImage("testdevice", "", "foo.bar", []byte("x")); err == nil {
		t.Error("Expected error for coalesceID with dot, got nil")
	}
}

func TestSavePushedImage_Anonymous(t *testing.T) {
	s := newTestServerAPI(t)
	deviceID := "testdevice2"

	pushedDir := filepath.Join(s.DataDir, "webp", deviceID, "pushed")

	// Save 5 anonymous pushes (no installID, no coalesceID) — all should remain
	for i := range 5 {
		imgData := fmt.Appendf(nil, "anonymous image %d", i)
		if err := s.savePushedImage(deviceID, "", "", imgData); err != nil {
			t.Fatalf("Failed to save anonymous push %d: %v", i, err)
		}
		time.Sleep(1 * time.Millisecond)
	}

	entries, err := os.ReadDir(pushedDir)
	if err != nil {
		t.Fatalf("Failed to read pushed dir: %v", err)
	}

	// All 5 files should remain (unbounded anonymous queue)
	// Anonymous: __{timestamp}.webp (only digits between __ and .webp)
	ephemeralCount := 0
	for _, entry := range entries {
		if isAnonymousEphemeral(entry.Name()) {
			ephemeralCount++
		}
	}
	if ephemeralCount != 5 {
		t.Errorf("Expected 5 anonymous ephemeral files, got %d: %v", ephemeralCount, entryNames(pushedDir))
	}
}

func TestSavePushedImage_InstallID_Replaces(t *testing.T) {
	s := newTestServerAPI(t)
	deviceID := "testdevice3"

	pushedDir := filepath.Join(s.DataDir, "webp", deviceID, "pushed")

	// Save 5 images with same installID
	for i := range 5 {
		imgData := fmt.Appendf(nil, "image %d", i)
		if err := s.savePushedImage(deviceID, "myapp", "", imgData); err != nil {
			t.Fatalf("Failed to save pushed image %d: %v", i, err)
		}
	}

	entries, err := os.ReadDir(pushedDir)
	if err != nil {
		t.Fatalf("Failed to read pushed dir: %v", err)
	}

	// With installID, file is always "myapp.webp" → overwrites → 1 file
	if len(entries) != 1 {
		t.Errorf("Expected 1 file with installID (always overwrites), got %d: %v", len(entries), entryNames(pushedDir))
	}
	if entries[0].Name() != "myapp.webp" {
		t.Errorf("Expected file 'myapp.webp', got '%s'", entries[0].Name())
	}
}

func entryNames(dir string) []string {
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// isAnonymousEphemeral returns true for __{timestamp}.webp files
// (no underscore between the timestamp and .webp).
func isAnonymousEphemeral(name string) bool {
	if !strings.HasPrefix(name, "__") || !strings.HasSuffix(name, ".webp") {
		return false
	}
	inner := name[2 : len(name)-5] // strip "__" and ".webp"
	return !strings.Contains(inner, "_")
}
