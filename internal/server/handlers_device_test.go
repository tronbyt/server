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
