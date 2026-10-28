package server

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/data"
	"tronbyt-server/internal/providers"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

func TestHandleAddAppPost(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	// Mock SystemAppsCache
	s.systemAppsCache = []apps.AppMetadata{
		{
			ID:                  "Clock",
			RecommendedInterval: 5,
			Path:                "system-apps/apps/clock",
		},
	}

	form := url.Values{}
	form.Add("name", "Clock")
	form.Add("path", "system-apps/apps/clock/clock.star")
	form.Add("uinterval", "10") // Default

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/addapp", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleAddAppPost)
	handler.ServeHTTP(rr, req)

	// Verify DB
	var app data.App
	if err := s.DB.First(&app, "name = ?", "Clock").Error; err != nil {
		t.Fatalf("App not created")
	}

	// Check that form value is used (10)
	if app.UInterval != 10 {
		t.Errorf("Expected uinterval 10 (from form), got %d", app.UInterval)
	}

	// Check Enabled default
	if !app.Enabled {
		t.Errorf("App should be enabled")
	}
}

func TestHandleAddAppGet_CustomAppFilters(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	s.systemAppsCache = []apps.AppMetadata{{ID: "Clock", Name: "Clock"}}

	appDir := filepath.Join(s.DataDir, "users", "testuser", "repo", "apps", "myclock")
	require.NoError(t, os.MkdirAll(appDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "myclock.star"), []byte("load()"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(appDir, "manifest.yaml"), []byte("category: time\ntags: [retro]\n"), 0o644))

	req, _ := http.NewRequest(http.MethodGet, "/devices/testdevice/addapp", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	http.HandlerFunc(s.handleAddAppGet).ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()

	// The custom section gets the same filter controls as the system section...
	for _, id := range []string{`id="custom_search"`, `id="category_custom_search"`, `id="hide_installed_custom_search"`, `id="show_broken_custom_search"`, `id="sort_custom_search"`} {
		assert.Contains(t, body, id)
	}
	// ...and its manifest metadata reaches the card for filtering.
	assert.Contains(t, body, `data-category="time"`)
	assert.Contains(t, body, `data-tags="retro"`)
	// Custom apps have no install counts, so only the system sort offers Popularity.
	assert.Equal(t, 1, strings.Count(body, `value="installs"`))
}

func TestHandleConfigAppPost(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)
	app := data.App{
		DeviceID: "testdevice",
		Iname:    "100",
		Name:     "TestApp",
		Enabled:  true,
	}
	s.DB.Create(&app)

	// JSON payload
	payload := `{"enabled": false, "autopin": true, "uinterval": 30, "display_time": 10, "notes": "Updated", "config": {"key": "val"}}`

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/100/config", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleConfigAppPost)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	var updatedApp data.App
	s.DB.First(&updatedApp, "iname = ?", "100")
	if updatedApp.Enabled {
		t.Errorf("App should be disabled")
	}
	if !updatedApp.AutoPin {
		t.Errorf("App should be auto-pinned")
	}
	if val, ok := updatedApp.Config["key"].(string); !ok || val != "val" {
		t.Errorf("App config not updated")
	}
}

func TestHandleConfigAppPost_TimeFormat(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)
	app := data.App{
		DeviceID: "testdevice",
		Iname:    "101",
		Name:     "TimeApp",
		Enabled:  true,
	}
	s.DB.Create(&app)

	// JSON payload with seconds in time
	payload := `{"start_time": "04:00:00", "end_time": "22:30:59"}`

	req, err := http.NewRequest(http.MethodPost, "/devices/testdevice/101/config", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleConfigAppPost)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	var updatedApp data.App
	s.DB.First(&updatedApp, "iname = ?", "101")

	if updatedApp.StartTime == nil {
		t.Error("Expected StartTime to be '04:00', but it was nil")
	} else if *updatedApp.StartTime != "04:00" {
		t.Errorf("Expected StartTime '04:00', got %q", *updatedApp.StartTime)
	}
	if updatedApp.EndTime == nil {
		t.Error("Expected EndTime to be '22:30', but it was nil")
	} else if *updatedApp.EndTime != "22:30" {
		t.Errorf("Expected EndTime '22:30', got %q", *updatedApp.EndTime)
	}
}

func TestHandleUploadAppPost_Zip(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	// Create a ZIP file in memory
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Add .star file
	f, err := zw.Create("testapp.star")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write([]byte("print('hello world')"))
	if err != nil {
		t.Fatal(err)
	}

	// Add .webp file (dummy content)
	f, err = zw.Create("testapp.webp")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write([]byte("fake image data"))
	if err != nil {
		t.Fatal(err)
	}

	// Add manifest.yaml
	f, err = zw.Create("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Write([]byte("packageName: manifest-app"))
	if err != nil {
		t.Fatal(err)
	}

	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	// Prepare Multipart Request
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "testapp.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(part, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/uploadapp", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleUploadAppPost)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	// Verify Extraction - should be in manifest-app based on packageName
	appDir := filepath.Join(s.DataDir, "users", "testuser", "apps", "manifest-app")
	if _, err := os.Stat(filepath.Join(appDir, "testapp.star")); os.IsNotExist(err) {
		t.Error("testapp.star not found in extracted dir (expected manifest-app dir)")
	}
	if _, err := os.Stat(filepath.Join(appDir, "testapp.webp")); os.IsNotExist(err) {
		t.Error("testapp.webp not found in extracted dir")
	}
	if _, err := os.Stat(filepath.Join(appDir, "manifest.yaml")); os.IsNotExist(err) {
		t.Error("manifest.yaml not found in extracted dir")
	}

	// Verify Preview - should match appName (manifest-app) inside appDir
	previewPath := filepath.Join(appDir, "manifest-app.webp")
	if _, err := os.Stat(previewPath); os.IsNotExist(err) {
		t.Errorf("Preview image not found in %s", previewPath)
	}
}

func TestHandleUploadAppPost_Zip_EdgeCases(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	tests := []struct {
		name           string
		zipContent     map[string]string
		zipFilename    string
		expectDirName  string // Name of the directory in users/testuser/apps/
		expectFiles    []string
		expectedStatus int
	}{
		{
			name: "No Manifest",
			zipContent: map[string]string{
				"script.star": "print('hello')",
			},
			zipFilename:    "nomanifest.zip",
			expectDirName:  "nomanifest",
			expectFiles:    []string{"script.star"},
			expectedStatus: http.StatusSeeOther,
		},
		{
			name: "Invalid PackageName",
			zipContent: map[string]string{
				"script.star":   "print('hello')",
				"manifest.yaml": "packageName: 'invalid name!'",
			},
			zipFilename:    "invalidpkg.zip",
			expectDirName:  "invalidpkg",
			expectFiles:    []string{"script.star", "manifest.yaml"},
			expectedStatus: http.StatusSeeOther,
		},
		{
			name: "Only Star",
			zipContent: map[string]string{
				"onlystar.star": "print('hello')",
			},
			zipFilename:    "onlystar.zip",
			expectDirName:  "onlystar",
			expectFiles:    []string{"onlystar.star"},
			expectedStatus: http.StatusSeeOther,
		},
		{
			name: "Only Webp",
			zipContent: map[string]string{
				"image.webp": "fake image data",
			},
			zipFilename:    "onlywebp.zip",
			expectDirName:  "onlywebp",
			expectFiles:    []string{"image.webp"},
			expectedStatus: http.StatusSeeOther,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create ZIP
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			for name, content := range tc.zipContent {
				f, err := zw.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.Write([]byte(content))
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := zw.Close(); err != nil {
				t.Fatal(err)
			}

			// Create Request
			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			part, err := writer.CreateFormFile("file", tc.zipFilename)
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(part, &buf)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/uploadapp", body)
			req.Header.Set("Content-Type", writer.FormDataContentType())

			ctx := context.WithValue(req.Context(), userContextKey, &user)
			ctx = context.WithValue(ctx, deviceContextKey, &device)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			handler := http.HandlerFunc(s.handleUploadAppPost)
			handler.ServeHTTP(rr, req)

			if rr.Code != tc.expectedStatus {
				t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, tc.expectedStatus)
			}

			if tc.expectDirName != "" {
				appDir := filepath.Join(s.DataDir, "users", "testuser", "apps", tc.expectDirName)
				if _, err := os.Stat(appDir); os.IsNotExist(err) {
					t.Errorf("App directory %s not found", appDir)
				}

				for _, f := range tc.expectFiles {
					if _, err := os.Stat(filepath.Join(appDir, f)); os.IsNotExist(err) {
						t.Errorf("File %s not found in app directory", f)
					}
				}
			}
		})
	}
}

func TestHandleUploadAppPost_CorruptedZip(t *testing.T) {
	s := newTestServer(t)
	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	// Create a corrupted zip (just random bytes)
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "corrupt.zip")
	if err != nil {
		t.Fatal(err)
	}
	_, err = part.Write([]byte("this is not a zip file"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/uploadapp", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleUploadAppPost)
	handler.ServeHTTP(rr, req)

	// Expecting 500 Internal Server Error because unzip fails
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("handler returned wrong status code for corrupt zip: got %v want %v", rr.Code, http.StatusInternalServerError)
	}
}

func TestHandleDeleteApp_UnpinsApp(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	iname := "app1"
	app := data.App{
		DeviceID: "testdevice",
		Iname:    iname,
		Name:     "Test App",
		Enabled:  true,
	}
	s.DB.Create(&app)

	// Pin the app
	if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(context.Background(), "pinned_app", iname); err != nil {
		t.Fatalf("failed to pin app: %v", err)
	}

	// Verify it is pinned
	var dev data.Device
	s.DB.First(&dev, "id = ?", "testdevice")
	if dev.PinnedApp == nil || *dev.PinnedApp != iname {
		t.Fatalf("Setup failed: app should be pinned")
	}

	// Create request
	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/"+iname+"/delete", nil)

	// Set context
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &dev) // Pass the device from DB
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleDeleteApp)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	// Verify app is deleted
	count, _ := gorm.G[data.App](s.DB).Where("device_id = ? AND iname = ?", "testdevice", iname).Count(context.Background(), "*")
	if count != 0 {
		t.Errorf("App was not deleted")
	}

	// Verify app is unpinned
	s.DB.First(&dev, "id = ?", "testdevice")
	if dev.PinnedApp != nil {
		t.Errorf("App was not unpinned, PinnedApp is: %v", *dev.PinnedApp)
	}
}

func TestHandleDeleteApp_NotPinned(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	iname := "app1"
	app := data.App{
		DeviceID: "testdevice",
		Iname:    iname,
		Name:     "Test App",
		Enabled:  true,
	}
	s.DB.Create(&app)

	// Ensure NOT pinned
	if _, err := gorm.G[data.Device](s.DB).Where("id = ?", device.ID).Update(context.Background(), "pinned_app", nil); err != nil {
		t.Fatalf("failed to unpin app: %v", err)
	}

	// Create request
	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/"+iname+"/delete", nil)

	// Set context
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleDeleteApp)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	// Verify app is deleted
	count, _ := gorm.G[data.App](s.DB).Where("device_id = ? AND iname = ?", "testdevice", iname).Count(context.Background(), "*")
	if count != 0 {
		t.Errorf("App was not deleted")
	}
}

func TestHandleDeleteApp_ClearsNightModeApp(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser", NightModeApp: "app1"}
	s.DB.Create(&device)

	iname := "app1"
	app := data.App{
		DeviceID: "testdevice",
		Iname:    iname,
		Name:     "Test App",
		Enabled:  true,
	}
	s.DB.Create(&app)

	// Verify NightModeApp is set
	var dev data.Device
	s.DB.First(&dev, "id = ?", "testdevice")
	if dev.NightModeApp != iname {
		t.Fatalf("Setup failed: NightModeApp should be set to %s, got %s", iname, dev.NightModeApp)
	}

	// Create request
	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/"+iname+"/delete", nil)

	// Set context
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &dev)
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleDeleteApp)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	// Verify NightModeApp is cleared
	s.DB.First(&dev, "id = ?", "testdevice")
	if dev.NightModeApp != "" {
		t.Errorf("NightModeApp was not cleared, is: %v", dev.NightModeApp)
	}
}

func TestMarkInstalledApps(t *testing.T) {
	s := &Server{
		DataDir: "/app/data",
	}

	systemApps := []apps.AppMetadata{
		{
			ID:       "weather",
			Name:     "Weather",
			FileName: "weather.star",
			Path:     "system-apps/apps/weather",
		},
		{
			ID:       "clock",
			Name:     "Clock",
			FileName: "clock.star",
			Path:     "system-apps/apps/clock",
		},
		{
			ID:       "oldapp",
			Name:     "Old App",
			FileName: "oldapp.star",
			Path:     "system-apps/apps/oldapp",
		},
		{
			ID:       "newapp",
			Name:     "New App",
			FileName: "newapp.star",
			Path:     "system-apps/apps/newapp",
		},
	}

	customApps := []apps.AppMetadata{
		{
			ID:       "mycustom",
			Name:     "My Custom App",
			FileName: "mycustom.star",
			Path:     "users/admin/apps/mycustom/mycustom.star",
		},
	}

	weatherPath := "system-apps/apps/weather"
	oldAppPath := "system-apps/apps/oldapp/oldapp.star"
	customAppPath := "users/admin/apps/mycustom"

	device := &data.Device{
		Apps: []*data.App{
			{
				Name: "Weather",
				Path: &weatherPath, // Directory format
			},
			{
				Name: "Old App",
				Path: &oldAppPath, // Legacy file format
			},
			{
				Name: "My Custom App",
				Path: &customAppPath, // Directory format for custom app
			},
			{
				Name: "New App",
				Path: nil, // Name match fallback
			},
		},
	}

	s.markInstalledApps(device, systemApps, customApps)

	// System Apps
	assert.True(t, systemApps[0].IsInstalled, "Weather should be installed (directory match)")
	assert.False(t, systemApps[1].IsInstalled, "Clock should not be installed")
	assert.True(t, systemApps[2].IsInstalled, "Old App should be installed (full path match)")
	assert.True(t, systemApps[3].IsInstalled, "New App should be installed (name match fallback)")

	// Custom Apps
	assert.True(t, customApps[0].IsInstalled, "My Custom App should be installed (directory match)")
}

func TestMarkInstalledApps_AbsolutePaths(t *testing.T) {
	s := &Server{
		DataDir: "/app/data",
	}

	systemApps := []apps.AppMetadata{
		{
			ID:       "weather",
			Name:     "Weather",
			FileName: "weather.star",
			Path:     "system-apps/apps/weather",
		},
	}

	absPath := "/app/data/system-apps/apps/weather/weather.star"

	device := &data.Device{
		Apps: []*data.App{
			{
				Name: "Weather",
				Path: &absPath, // Absolute path in DB
			},
		},
	}

	s.markInstalledApps(device, systemApps, nil)

	assert.True(t, systemApps[0].IsInstalled, "Weather should be installed (absolute to relative conversion match)")
}

// Toggling broken used to write the manifest back from a struct holding only
// broken/brokenReason, which destroyed every other key in the file - id, name,
// supports2x, tags, all of it. The whole point of the endpoint is a two-field
// edit, so the rest of the file has to survive it verbatim.
func TestUpdateAppBrokenStatusPreservesTheRestOfTheManifest(t *testing.T) {
	s := newTestServer(t)

	const original = `id: repro-demo
name: Repro Demo
author: bench
recommendedInterval: 30
supports2x: true
tags:
  - demo
published: "2026-01-01"
`
	appDir := filepath.Join(s.DataDir, "system-apps", "apps", "repro_demo")
	require.NoError(t, os.MkdirAll(appDir, 0o755))
	manifestPath := filepath.Join(appDir, "manifest.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte(original), 0o644))

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)

	toggle := func(path string, broken bool) {
		req, _ := http.NewRequest(http.MethodPost,
			path+"?app_name=repro_demo&package_name=repro_demo&broken_reason=testing", nil)
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, &user))
		rr := httptest.NewRecorder()
		s.updateAppBrokenStatus(rr, req, broken)
		require.Equal(t, http.StatusOK, rr.Code)
	}

	assertSurvived := func(stage string) {
		t.Helper()
		var manifest map[string]any
		raw, err := os.ReadFile(manifestPath)
		require.NoError(t, err)
		require.NoError(t, yaml.Unmarshal(raw, &manifest))
		for key, want := range map[string]any{
			"id":                  "repro-demo",
			"name":                "Repro Demo",
			"author":              "bench",
			"recommendedInterval": 30,
			"supports2x":          true,
			"published":           "2026-01-01",
		} {
			assert.Equalf(t, want, manifest[key], "%s: %q was lost or altered", stage, key)
		}
		assert.Equalf(t, []any{"demo"}, manifest["tags"], "%s: tags were lost", stage)
	}

	toggle("/mark_app_broken", true)
	assertSurvived("after marking broken")
	var marked map[string]any
	raw, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &marked))
	assert.Equal(t, true, marked["broken"])
	assert.Equal(t, "testing", marked["brokenReason"])

	toggle("/unmark_app_broken", false)
	assertSurvived("after unmarking broken")
	var cleared map[string]any
	raw, err = os.ReadFile(manifestPath)
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &cleared))
	assert.Equal(t, false, cleared["broken"])
	assert.Equal(t, "", cleared["brokenReason"])
}

func TestExternalAppThumbnail_OnDemandAndCached(t *testing.T) {
	s := newTestServer(t)

	chunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 1000},
	}
	mock := &mockExternalProvider{
		name:   "screeny",
		chunks: chunks,
		apps: []providers.ExternalAppMeta{
			{ID: "clock_test", Name: "Clock Test"},
		},
	}
	s.RegisterExternalProvider(mock)

	// 1. Initial request - renders preview on demand and writes cache
	req, _ := http.NewRequest(http.MethodGet, "/preview/app/screeny:clock_test", nil)
	req.SetPathValue("id", "screeny:clock_test")
	rr := httptest.NewRecorder()

	s.handleAppThumbnail(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "image/webp", rr.Header().Get("Content-Type"))
	assert.Equal(t, dummyChunkWebP1, rr.Body.Bytes())

	// Verify disk cache exists
	cachePath := filepath.Join(s.DataDir, "cache", "previews", "screeny", "clock_test.webp")
	cachedBytes, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	assert.Equal(t, dummyChunkWebP1, cachedBytes)

	// 2. Second request - serves directly from disk cache
	rr2 := httptest.NewRecorder()
	s.handleAppThumbnail(rr2, req)
	assert.Equal(t, http.StatusOK, rr2.Code)
	assert.Equal(t, dummyChunkWebP1, rr2.Body.Bytes())

	// 3. Third request with ?refresh=true - re-renders and updates cache
	mock.chunks = []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP2, DurationMs: 1000},
	}
	reqRefresh, _ := http.NewRequest(http.MethodGet, "/preview/app/screeny:clock_test?refresh=true", nil)
	reqRefresh.SetPathValue("id", "screeny:clock_test")
	rr3 := httptest.NewRecorder()
	s.handleAppThumbnail(rr3, reqRefresh)
	assert.Equal(t, http.StatusOK, rr3.Code)
	assert.Equal(t, dummyChunkWebP2, rr3.Body.Bytes())

	cachedBytesRefreshed, err := os.ReadFile(cachePath)
	require.NoError(t, err)
	assert.Equal(t, dummyChunkWebP2, cachedBytesRefreshed)
}

func TestExternalAppConfigPreview_WithAndWithoutConfig(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	extPath := "screeny:clock_test"
	app := data.App{
		DeviceID: device.ID,
		Iname:    "testapp",
		Name:     "Clock Test",
		Path:     &extPath,
	}
	s.DB.Create(&app)

	chunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 1500},
	}
	mock := &mockExternalProvider{
		name:   "screeny",
		chunks: chunks,
		apps: []providers.ExternalAppMeta{
			{ID: "clock_test", Name: "Clock Test"},
		},
	}
	s.RegisterExternalProvider(mock)

	// 1. Config preview without config query param
	req, _ := http.NewRequest(http.MethodGet, "/devices/testdevice/installations/testapp/preview", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	s.handleRenderConfigPreview(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "image/webp", rr.Header().Get("Content-Type"))
	assert.Equal(t, dummyChunkWebP1, rr.Body.Bytes())

	// 2. Config preview with config query param
	req2, _ := http.NewRequest(http.MethodGet, "/devices/testdevice/installations/testapp/preview?config=%7B%22speed%22%3A5%7D", nil)
	req2 = req2.WithContext(ctx)

	rr2 := httptest.NewRecorder()
	s.handleRenderConfigPreview(rr2, req2)

	assert.Equal(t, http.StatusOK, rr2.Code)
	assert.Equal(t, "image/webp", rr2.Header().Get("Content-Type"))
	assert.Equal(t, dummyChunkWebP1, rr2.Body.Bytes())
}

func TestExternalAppConfigPreview_FullAnimationServing(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	extPath := "screeny:clock_full"
	app := data.App{
		DeviceID: device.ID,
		Iname:    "testapp_full",
		Name:     "Clock Full",
		Path:     &extPath,
	}
	s.DB.Create(&app)

	fullAnimationBytes := []byte("full-length-complete-animation")
	chunks := []providers.RenderChunk{
		{
			WebPBytes:     dummyChunkWebP1,
			DurationMs:    1500,
			FullWebPBytes: fullAnimationBytes,
		},
		{
			WebPBytes:  dummyChunkWebP2,
			DurationMs: 1500,
		},
	}
	mock := &mockExternalProvider{
		name:   "screeny",
		chunks: chunks,
		apps: []providers.ExternalAppMeta{
			{ID: "clock_full", Name: "Clock Full"},
		},
	}
	s.RegisterExternalProvider(mock)

	// Config preview on dynamic render should serve FullWebPBytes
	req, _ := http.NewRequest(http.MethodGet, "/devices/testdevice/installations/testapp_full/preview?config=%7B%7D", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	s.handleRenderConfigPreview(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, fullAnimationBytes, rr.Body.Bytes(), "Must serve complete unchunked animation for browser preview")

	// Save app chunks and verify primaryPath contains chunk 0 (safe for device) and _full.webp contains full animation
	require.NoError(t, s.saveAppChunks(device.ID, &app, chunks))

	deviceWebpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	primaryPath := s.getAppWebpPath(deviceWebpDir, &app)
	savedPrimary, err := os.ReadFile(primaryPath)
	require.NoError(t, err)
	assert.Equal(t, dummyChunkWebP1, savedPrimary, "primary webp must contain chunk 0 safe for device")

	fullPath := filepath.Join(deviceWebpDir, "Clock Full-testapp_full_full.webp")
	savedFull, err := os.ReadFile(fullPath)
	require.NoError(t, err)
	assert.Equal(t, fullAnimationBytes, savedFull, "_full.webp must contain complete animation for browser")

	// Verify getAppChunks still yields the individual hardware chunks
	loadedChunks := s.getAppChunks(device.ID, &app)
	require.Len(t, loadedChunks, 2)
	assert.Equal(t, dummyChunkWebP1, loadedChunks[0].WebPBytes)
	assert.Equal(t, dummyChunkWebP2, loadedChunks[1].WebPBytes)
}

func TestExternalAppConfigGet(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	extPath := "screeny:clock_test"
	app := data.App{
		DeviceID: device.ID,
		Iname:    "testapp",
		Name:     "Clock Test",
		Path:     &extPath,
	}
	s.DB.Create(&app)

	mock := &mockExternalProvider{
		name:   "screeny",
		chunks: []providers.RenderChunk{{WebPBytes: dummyChunkWebP1, DurationMs: 1500}},
		apps: []providers.ExternalAppMeta{
			{ID: "clock_test", Name: "Clock Test"},
		},
	}
	s.RegisterExternalProvider(mock)

	req, _ := http.NewRequest(http.MethodGet, "/devices/testdevice/testapp/config", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	s.handleConfigAppGet(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, "schemaConfigContainer")
	assert.Contains(t, body, "let schema =")
}

func TestExternalAppPushPreview(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	extPath := "screeny:clock_test"
	app := data.App{
		DeviceID: device.ID,
		Iname:    "testapp",
		Name:     "Clock Test",
		Path:     &extPath,
	}
	s.DB.Create(&app)

	chunks := []providers.RenderChunk{
		{WebPBytes: dummyChunkWebP1, DurationMs: 10000},
	}
	mock := &mockExternalProvider{
		name:   "screeny",
		chunks: chunks,
	}
	s.RegisterExternalProvider(mock)

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/testapp/preview", strings.NewReader(`{"test":1}`))
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	ctx = context.WithValue(ctx, appContextKey, &app)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	s.handlePushPreview(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	// Verify pushed image was saved
	webpDir, err := s.ensureDeviceImageDir(device.ID)
	require.NoError(t, err)
	pushedFile := filepath.Join(webpDir, "pushed", app.Iname+".webp")
	b, err := os.ReadFile(pushedFile)
	require.NoError(t, err)
	assert.Equal(t, dummyChunkWebP1, b)
}

func TestExternalAppGetMetadata(t *testing.T) {
	s := newTestServer(t)

	mock := &mockExternalProvider{
		name: "screeny",
		apps: []providers.ExternalAppMeta{
			{
				ID:      "clocks-minimal",
				Name:    "Minimal Clock",
				Summary: "A clean minimalist clock",
				Desc:    "Detailed clock description",
				Author:  "TestAuthor",
				Tags:    []string{"clock", "minimal"},
			},
		},
	}
	s.RegisterExternalProvider(mock)

	meta := s.getAppMetadata(context.Background(), "screeny:clocks-minimal")
	require.NotNil(t, meta)
	assert.Equal(t, "screeny:clocks-minimal", meta.ID)
	assert.Equal(t, "Minimal Clock", meta.Name)
	assert.Equal(t, "A clean minimalist clock", meta.Summary)
	assert.Equal(t, "Detailed clock description", meta.Desc)
	assert.Equal(t, "TestAuthor", meta.Author)
	assert.Equal(t, "Clocks", meta.Category)
	assert.Equal(t, "screeny", meta.Provider)
	assert.Equal(t, 10, meta.RecommendedInterval)
}

func TestExternalAppUIntervalFallbackAndNonBlockingAdd(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	mock := &mockExternalProvider{
		name:   "screeny",
		chunks: []providers.RenderChunk{{WebPBytes: dummyChunkWebP1, DurationMs: 1000}},
		apps: []providers.ExternalAppMeta{
			{ID: "flock", Name: "Flock"},
		},
	}
	s.RegisterExternalProvider(mock)

	// Test 1: handleAddAppPost with uinterval="0" - should default to 10 and redirect immediately
	form := url.Values{}
	form.Add("name", "Flock")
	form.Add("path", "screeny:flock")
	form.Add("uinterval", "0") // 0 should be corrected to 10

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/addapp", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	s.handleAddAppPost(rr, req)

	assert.Equal(t, http.StatusSeeOther, rr.Code)
	redirectLocation := rr.Header().Get("Location")
	assert.Contains(t, redirectLocation, "/config?delete_on_cancel=true")

	var savedApp data.App
	err := s.DB.Where("device_id = ? AND name = ?", device.ID, "Flock").First(&savedApp).Error
	require.NoError(t, err)
	assert.Equal(t, 10, savedApp.UInterval) // verified uinterval corrected to 10

	// Test 2: In-flight deduplication in possiblyRender
	appKey := fmt.Sprintf("%s:%s", device.ID, savedApp.Iname)
	doneCh := make(chan struct{})
	s.inFlightRendersMu.Lock()
	s.inFlightRenders[appKey] = doneCh
	s.inFlightRendersMu.Unlock()

	var saveWg sync.WaitGroup
	// Simulate in-flight render completing and saving chunks
	saveWg.Go(func() {
		_ = s.saveAppChunks(device.ID, &savedApp, []providers.RenderChunk{
			{WebPBytes: []byte("fake_webp"), DurationMs: 15000},
		})
		s.inFlightRendersMu.Lock()
		delete(s.inFlightRenders, appKey)
		close(doneCh)
		s.inFlightRendersMu.Unlock()
	})

	// While in flight, possiblyRender should wait for completion and return true without triggering a second render
	rendered := s.possiblyRender(context.Background(), &savedApp, &device, &user)
	assert.True(t, rendered)

	saveWg.Wait()

	// Wait for any background renders to complete before test cleanup
	s.backgroundRenders.Wait()
}

func TestExternalAppSchema_ConditionedOnDeviceType(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)

	device64x32 := data.Device{ID: "dev64", Username: "testuser", Type: data.DeviceTidbytGen1, APIKey: "key64"}
	s.DB.Create(&device64x32)

	device128x64 := data.Device{ID: "dev128", Username: "testuser", Type: data.DeviceRaspberryPiWide, APIKey: "key128"}
	s.DB.Create(&device128x64)

	extPath := "screeny:clock"
	app64 := data.App{
		DeviceID: device64x32.ID,
		Iname:    "clock64",
		Name:     "Clock",
		Path:     &extPath,
	}
	s.DB.Create(&app64)

	app128 := data.App{
		DeviceID: device128x64.ID,
		Iname:    "clock128",
		Name:     "Clock",
		Path:     &extPath,
	}
	s.DB.Create(&app128)

	mock := &mockExternalProvider{
		name: "screeny",
		apps: []providers.ExternalAppMeta{{ID: "clock", Name: "Clock"}},
	}
	s.RegisterExternalProvider(mock)

	// 1. 64x32 Device: schema must NOT contain scale_filter
	req64, _ := http.NewRequest(http.MethodGet, "/devices/dev64/clock64/schema", nil)
	ctx64 := context.WithValue(req64.Context(), userContextKey, &user)
	ctx64 = context.WithValue(ctx64, deviceContextKey, &device64x32)
	ctx64 = context.WithValue(ctx64, appContextKey, &app64)
	req64 = req64.WithContext(ctx64)

	rr64 := httptest.NewRecorder()
	s.handleAppSchemaGet(rr64, req64)
	assert.Equal(t, http.StatusOK, rr64.Code)
	assert.Contains(t, rr64.Body.String(), "param1")
	assert.NotContains(t, rr64.Body.String(), "scale_filter")

	// 2. 128x64 Device (Supports2x): schema MUST contain scale_filter
	req128, _ := http.NewRequest(http.MethodGet, "/devices/dev128/clock128/schema", nil)
	ctx128 := context.WithValue(req128.Context(), userContextKey, &user)
	ctx128 = context.WithValue(ctx128, deviceContextKey, &device128x64)
	ctx128 = context.WithValue(ctx128, appContextKey, &app128)
	req128 = req128.WithContext(ctx128)

	rr128 := httptest.NewRecorder()
	s.handleAppSchemaGet(rr128, req128)
	assert.Equal(t, http.StatusOK, rr128.Code)
	assert.Contains(t, rr128.Body.String(), "param1")
	assert.Contains(t, rr128.Body.String(), "scale_filter")
}

func TestHandleCurrentApp_ETagAnd304(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	extPath := "screeny:testapp"
	app := data.App{
		DeviceID: device.ID,
		Iname:    "testapp",
		Name:     "Test App",
		Path:     &extPath,
	}
	s.DB.Create(&app)

	// Save a dummy chunk
	chunks := []providers.RenderChunk{{WebPBytes: dummyChunkWebP1, DurationMs: 15000}}
	require.NoError(t, s.saveAppChunks(device.ID, &app, chunks))

	// Initial GET without ETag
	req, _ := http.NewRequest(http.MethodGet, "/devices/testdevice/current", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	s.handleCurrentApp(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "image/webp", rr.Header().Get("Content-Type"))
	etag := rr.Header().Get("ETag")
	assert.NotEmpty(t, etag)

	// Subsequent GET with matching If-None-Match should return 304 Not Modified
	req2, _ := http.NewRequest(http.MethodGet, "/devices/testdevice/current", nil)
	req2.Header.Set("If-None-Match", etag)
	req2 = req2.WithContext(ctx)

	rr2 := httptest.NewRecorder()
	s.handleCurrentApp(rr2, req2)

	assert.Equal(t, http.StatusNotModified, rr2.Code)
	assert.Empty(t, rr2.Body.Bytes())
}

func TestExternalAppThumbnail_CacheInvalidationOnV4(t *testing.T) {
	s := newTestServer(t)

	// Pre-create cache dir with older .unblended_v3 marker and stale thumbnail
	cacheDir := filepath.Join(s.DataDir, "cache", "previews", "screeny")
	require.NoError(t, os.MkdirAll(cacheDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, ".unblended_v3"), []byte("v3"), 0644))
	staleThumbnail := []byte("stale-corrupted-thumbnail")
	require.NoError(t, os.WriteFile(filepath.Join(cacheDir, "ghosts.webp"), staleThumbnail, 0644))

	// Pre-populate an app in DB with screeny path and non-zero LastRender
	extPath := "screeny:ghosts"
	now := time.Now()
	app := data.App{
		DeviceID:   "testdevice",
		Iname:      "ghostsiname",
		Name:       "Ghosts",
		Path:       &extPath,
		LastRender: now,
	}
	s.DB.Create(&app)

	// Register provider with fresh chunk
	freshChunk := []byte("fresh-unblended-chunk")
	mock := &mockExternalProvider{
		name:   "screeny",
		chunks: []providers.RenderChunk{{WebPBytes: freshChunk, DurationMs: 1000}},
		apps:   []providers.ExternalAppMeta{{ID: "ghosts", Name: "Ghosts"}},
	}
	s.RegisterExternalProvider(mock)

	// Request thumbnail
	req, _ := http.NewRequest(http.MethodGet, "/preview/app/screeny:ghosts", nil)
	req.SetPathValue("id", "screeny:ghosts")
	rr := httptest.NewRecorder()
	s.handleAppThumbnail(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, freshChunk, rr.Body.Bytes())

	// Verify .unblended_v3 is removed and .previews_v9 exists
	_, errV3 := os.Stat(filepath.Join(cacheDir, ".unblended_v3"))
	assert.True(t, os.IsNotExist(errV3))
	_, errV4 := os.Stat(filepath.Join(cacheDir, ".previews_v4"))
	assert.True(t, os.IsNotExist(errV4))
	_, errV9 := os.Stat(filepath.Join(cacheDir, ".previews_v9"))
	assert.NoError(t, errV9)

	// Verify app's LastRender was reset to zero time
	var updatedApp data.App
	require.NoError(t, s.DB.Where("id = ?", app.ID).First(&updatedApp).Error)
	assert.True(t, updatedApp.LastRender.IsZero())
}

func TestExternalAppThumbnail_SingleFlightDeduplication(t *testing.T) {
	s := newTestServer(t)

	mock := &mockExternalProvider{
		name:   "screeny",
		chunks: []providers.RenderChunk{{WebPBytes: dummyChunkWebP1, DurationMs: 1000}},
		apps:   []providers.ExternalAppMeta{{ID: "parallel_app", Name: "Parallel App"}},
	}
	s.RegisterExternalProvider(mock)

	var wg sync.WaitGroup
	errCh := make(chan error, 5)

	for range 5 {
		wg.Go(func() {
			req, _ := http.NewRequest(http.MethodGet, "/preview/app/screeny:parallel_app", nil)
			req.SetPathValue("id", "screeny:parallel_app")
			rr := httptest.NewRecorder()
			s.handleAppThumbnail(rr, req)
			if rr.Code != http.StatusOK {
				errCh <- fmt.Errorf("unexpected status code: %d", rr.Code)
			}
			if !bytes.Equal(rr.Body.Bytes(), dummyChunkWebP1) {
				errCh <- fmt.Errorf("unexpected body content")
			}
		})
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		assert.NoError(t, err)
	}
}
