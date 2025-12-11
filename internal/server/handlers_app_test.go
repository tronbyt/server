package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"tronbyt-server/internal/apps"
	"tronbyt-server/internal/data"
)

func TestHandleAddAppPost(t *testing.T) {
	s := newTestServer(t)

	user := data.User{Username: "testuser"}
	s.DB.Create(&user)
	device := data.Device{ID: "testdevice", Username: "testuser"}
	s.DB.Create(&device)

	// Mock SystemAppsCache
	s.SystemAppsCache = []apps.AppMetadata{
		{ID: "Clock", RecommendedInterval: 5},
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
