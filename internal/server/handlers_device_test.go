package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"tronbyt-server/internal/data"
)

func TestHandleCreateDevicePost(t *testing.T) {
	s := newTestServer(t)

	// Create user
	user := data.User{Username: "testuser"}
	s.DB.Create(&user)

	// Prepare form data
	form := url.Values{}
	form.Add("name", "New Device")
	form.Add("device_type", "0")
	form.Add("brightness", "2")

	req, _ := http.NewRequest(http.MethodPost, "/devices/create", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Inject user into context (simulating RequireLogin)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleCreateDevicePost)
	handler.ServeHTTP(rr, req)

	// Check redirect to dashboard
	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	// Verify DB
	var device data.Device
	if err := s.DB.First(&device, "name = ?", "New Device").Error; err != nil {
		t.Fatalf("Device not created in DB")
	}
	if device.Username != "testuser" {
		t.Errorf("Device username mismatch")
	}
}

func TestHandleUpdateDevicePost(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{
		ID:         "testdevice",
		Username:   "testuser",
		Name:       "Old Name",
		Brightness: data.Brightness(20),
	}
	s.DB.Create(&device)

	form := url.Values{}
	form.Add("name", "New Name")
	form.Add("device_type", "1")
	form.Add("brightness", "5")
	form.Add("default_interval", "10")
	form.Add("color_filter", "redshift")

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleUpdateDevicePost)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	var updatedDevice data.Device
	s.DB.First(&updatedDevice, "id = ?", "testdevice")
	if updatedDevice.Name != "New Name" {
		t.Errorf("Name not updated")
	}
	if *updatedDevice.ColorFilter != "redshift" {
		t.Errorf("Color filter not updated")
	}
}

func TestHandleDeleteDevice(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)
	app := data.App{DeviceID: "testdevice", Name: "TestApp", Iname: "100"}
	s.DB.Create(&app)

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/delete", nil)
	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleDeleteDevice)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusSeeOther)
	}

	var count int64
	s.DB.Model(&data.Device{}).Where("id = ?", "testdevice").Count(&count)
	if count != 0 {
		t.Errorf("Device not deleted")
	}
	s.DB.Model(&data.App{}).Where("device_id = ?", "testdevice").Count(&count)
	if count != 0 {
		t.Errorf("App not deleted (cascade failed)")
	}
}

func TestHandleUpdateBrightness(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{
		ID:         "testdevice",
		Username:   "testuser",
		Name:       "Test Device",
		Brightness: data.Brightness(20),
	}
	s.DB.Create(&device)

	bUI := 4 // Define bUI here

	form := url.Values{}
	form.Add("brightness", strconv.Itoa(bUI))

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update_brightness", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleUpdateBrightness)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	var updatedDevice data.Device
	s.DB.First(&updatedDevice, "id = ?", "testdevice")

	expectedBrightness := data.BrightnessFromUIScale(bUI, nil) // For UI 4, this should be 35
	if updatedDevice.Brightness != expectedBrightness {
		t.Errorf("Brightness not updated correctly: got %v want %v", updatedDevice.Brightness, expectedBrightness)
	}
}

func TestHandleUpdateBrightness_Invalid(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{
		ID:         "testdevice",
		Username:   "testuser",
		Name:       "Test Device",
		Brightness: data.Brightness(20),
	}
	s.DB.Create(&device)

	testCases := []string{"-1", "6", "abc"}

	for _, tc := range testCases {
		form := url.Values{}
		form.Add("brightness", tc)

		req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update_brightness", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		ctx := context.WithValue(req.Context(), userContextKey, &user)
		ctx = context.WithValue(ctx, deviceContextKey, &device)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(s.handleUpdateBrightness)
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("handler returned wrong status code for input %s: got %v want %v", tc, rr.Code, http.StatusBadRequest)
		}
	}
}

func TestHandleUpdateInterval(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{
		ID:              "testdevice",
		Username:        "testuser",
		DefaultInterval: 15,
	}
	s.DB.Create(&device)

	form := url.Values{}
	form.Add("interval", "30")

	req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update_interval", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	ctx := context.WithValue(req.Context(), userContextKey, &user)
	ctx = context.WithValue(ctx, deviceContextKey, &device)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler := http.HandlerFunc(s.handleUpdateInterval)
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	var updatedDevice data.Device
	s.DB.First(&updatedDevice, "id = ?", "testdevice")
	if updatedDevice.DefaultInterval != 30 {
		t.Errorf("Interval not updated, got %d", updatedDevice.DefaultInterval)
	}
}

func TestHandleUpdateInterval_Invalid(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{
		ID:              "testdevice",
		Username:        "testuser",
		DefaultInterval: 15,
	}
	s.DB.Create(&device)

	testCases := []string{"-1", "0", "abc"}

	for _, tc := range testCases {
		form := url.Values{}
		form.Add("interval", tc)

		req, _ := http.NewRequest(http.MethodPost, "/devices/testdevice/update_interval", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		ctx := context.WithValue(req.Context(), userContextKey, &user)
		ctx = context.WithValue(ctx, deviceContextKey, &device)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(s.handleUpdateInterval)
		handler.ServeHTTP(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("handler returned wrong status code for input %s: got %v want %v", tc, rr.Code, http.StatusBadRequest)
		}
		if !strings.Contains(rr.Body.String(), "Interval must be 1 or greater") {
			t.Errorf("handler returned wrong error message for input %s: got %s want %s", tc, rr.Body.String(), "Interval must be 1 or greater")
		}
	}
}
