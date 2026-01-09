package server

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
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
			Manifest: apps.Manifest{
				ID:                  "Clock",
				RecommendedInterval: 5,
			},
			Path: "system-apps/apps/clock",
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

	// Check recommended interval logic (10 -> 5)
	if app.UInterval != 5 {
		t.Errorf("Expected uinterval 5 (recommended), got %d", app.UInterval)
	}

	// Check Enabled default
	if !app.Enabled {
		t.Errorf("App should be enabled")
	}
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
			Manifest: apps.Manifest{
				ID:       "weather",
				Name:     "Weather",
				FileName: "weather.star",
			},
			Path: "system-apps/apps/weather",
		},
		{
			Manifest: apps.Manifest{
				ID:       "clock",
				Name:     "Clock",
				FileName: "clock.star",
			},
			Path: "system-apps/apps/clock",
		},
		{
			Manifest: apps.Manifest{
				ID:       "oldapp",
				Name:     "Old App",
				FileName: "oldapp.star",
			},
			Path: "system-apps/apps/oldapp",
		},
		{
			Manifest: apps.Manifest{
				ID:       "newapp",
				Name:     "New App",
				FileName: "newapp.star",
			},
			Path: "system-apps/apps/newapp",
		},
	}

	customApps := []apps.AppMetadata{
		{
			Manifest: apps.Manifest{
				ID:       "mycustom",
				Name:     "My Custom App",
				FileName: "mycustom.star",
			},
			Path: "users/admin/apps/mycustom/mycustom.star",
		},
	}

	weatherPath := "system-apps/apps/weather"
	oldAppPath := "system-apps/apps/oldapp/oldapp.star"
	customAppPath := "users/admin/apps/mycustom"

	device := &data.Device{
		Apps: []data.App{
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
			Manifest: apps.Manifest{
				ID:       "weather",
				Name:     "Weather",
				FileName: "weather.star",
			},
			Path: "system-apps/apps/weather",
		},
	}

	absPath := "/app/data/system-apps/apps/weather/weather.star"

	device := &data.Device{
		Apps: []data.App{
			{
				Name: "Weather",
				Path: &absPath, // Absolute path in DB
			},
		},
	}

	s.markInstalledApps(device, systemApps, nil)

	assert.True(t, systemApps[0].IsInstalled, "Weather should be installed (absolute to relative conversion match)")
}
