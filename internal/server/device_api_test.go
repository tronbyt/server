package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tronbyt-server/internal/data"
)

func TestHandleNextApp(t *testing.T) {
	s := newTestServerAPI(t)
	var device data.Device
	s.DB.First(&device, "id = ?", "testdevice")

	app := data.App{
		DeviceID:  "testdevice",
		Iname:     "testapp",
		Name:      "Test App",
		UInterval: 10,
		Enabled:   true,
		Pushed:    true,
	}
	if err := s.DB.Create(&app).Error; err != nil {
		t.Fatalf("Failed to create app: %v", err)
	}

	if err := s.savePushedImage("testdevice", "testapp", []byte("dummy image")); err != nil {
		t.Fatalf("Failed to save pushed image: %v", err)
	}

	req := httptest.NewRequest("GET", "/testdevice/next", nil)
	req.SetPathValue("id", "testdevice")

	rr := httptest.NewRecorder()

	s.handleNextApp(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusOK)
	}

	if rr.Header().Get("Content-Type") != "image/webp" {
		t.Errorf("Expected content type image/webp, got %s", rr.Header().Get("Content-Type"))
	}
}
