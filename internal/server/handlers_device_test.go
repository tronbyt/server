package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	form.Add("device_type", "tidbyt_gen1")
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
	form.Add("device_type", "tidbyt_gen2")
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
