package server

import (
	"context"
	"testing"

	"tronbyt-server/internal/data"
)

func TestDetermineNextApp_NightMode(t *testing.T) {
	s := newTestServer(t)

	// Create a user
	user := data.User{Username: "testuser"}
	if err := s.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create a device with Night Mode enabled all day
	device := data.Device{
		ID:               "device1",
		Username:         user.Username,
		NightModeEnabled: true,
		NightStart:       "00:00",
		NightEnd:         "23:59",
		NightModeApp:     "app-night",
	}
	if err := s.DB.Create(&device).Error; err != nil {
		t.Fatalf("failed to create device: %v", err)
	}

	// Create Regular App
	appRegular := data.App{
		DeviceID: device.ID,
		Iname:    "app-regular",
		Name:     "Regular App",
		Enabled:  true,
		Pushed:   true, // Bypass rendering check
		Order:    1,
	}
	if err := s.DB.Create(&appRegular).Error; err != nil {
		t.Fatalf("failed to create regular app: %v", err)
	}

	// Create Night Mode App
	appNight := data.App{
		DeviceID: device.ID,
		Iname:    "app-night",
		Name:     "Night App",
		Enabled:  false, // Usually disabled for day rotation
		Pushed:   true,  // Bypass rendering check
		Order:    2,
	}
	if err := s.DB.Create(&appNight).Error; err != nil {
		t.Fatalf("failed to create night app: %v", err)
	}

	// Reload device with apps
	var d data.Device
	if err := s.DB.Preload("Apps").First(&d, "id = ?", device.ID).Error; err != nil {
		t.Fatalf("failed to reload device with apps: %v", err)
	}

	// Helper to check what app is selected
	// We want to ensure that ONLY appNight is selected.
	// Since determineNextApp rotates based on LastAppIndex, we should try a few times.

	// Start from index -1 (so next is 0)
	d.LastAppIndex = -1

	for i := range 10 {
		app, nextIndex, err := s.determineNextApp(context.Background(), &d, &user)
		if err != nil {
			t.Fatalf("determineNextApp failed: %v", err)
		}
		if app == nil {
			t.Fatalf("expected an app, got nil")
		}

		if app.Iname != "app-night" {
			t.Errorf("Iteration %d: Expected Night Mode app (app-night), got %s", i, app.Iname)
		}

		// Update LastAppIndex for next iteration to simulate rotation
		d.LastAppIndex = nextIndex
	}
}

func TestDetermineNextApp_NightMode_NoAppSelected(t *testing.T) {
	s := newTestServer(t)

	// Create a user
	user := data.User{Username: "testuser"}
	if err := s.DB.Create(&user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Create a device with Night Mode enabled all day but NO NightModeApp selected
	device := data.Device{
		ID:               "device2",
		Username:         user.Username,
		NightModeEnabled: true,
		NightStart:       "00:00",
		NightEnd:         "23:59",
		NightModeApp:     "", // Empty!
	}
	if err := s.DB.Create(&device).Error; err != nil {
		t.Fatalf("failed to create device: %v", err)
	}

	// Create Regular App
	appRegular := data.App{
		DeviceID: device.ID,
		Iname:    "app-regular",
		Name:     "Regular App",
		Enabled:  true,
		Pushed:   true,
		Order:    1,
	}
	if err := s.DB.Create(&appRegular).Error; err != nil {
		t.Fatalf("failed to create regular app: %v", err)
	}

	// Reload device with apps
	var d data.Device
	if err := s.DB.Preload("Apps").First(&d, "id = ?", device.ID).Error; err != nil {
		t.Fatalf("failed to reload device with apps: %v", err)
	}

	d.LastAppIndex = -1

	// Should return the regular app
	app, _, err := s.determineNextApp(context.Background(), &d, &user)
	if err != nil {
		t.Fatalf("determineNextApp failed: %v", err)
	}
	if app == nil {
		t.Fatal("expected an app (fallback to rotation), got nil")
	}

	if app.Iname != "app-regular" {
		t.Errorf("Expected regular app (app-regular), got %s", app.Iname)
	}
}
