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

	req := httptest.NewRequest(http.MethodGet, "/testdevice/next", nil)
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

func TestHandleNextApp_FirmwareUpdate(t *testing.T) {
	s := newTestServerAPI(t)

	// Ensure device exists
	device := data.Device{
		ID:       "fwdevice",
		Username: "admin",
		Info: data.DeviceInfo{
			ProtocolType: data.ProtocolWS, // Start with WS to test protocol update to HTTP
		},
	}
	s.DB.Create(&device)

	req := httptest.NewRequest(http.MethodGet, "/fwdevice/next", nil)
	req.SetPathValue("id", "fwdevice")
	req.Header.Set("X-Firmware-Version", "v1.5.0")

	rr := httptest.NewRecorder()
	s.handleNextApp(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	// Verify DB update
	var updatedDevice data.Device
	if err := s.DB.First(&updatedDevice, "id = ?", "fwdevice").Error; err != nil {
		t.Fatalf("Failed to fetch device: %v", err)
	}

	if updatedDevice.Info.ProtocolType != data.ProtocolHTTP {
		t.Errorf("Expected protocol HTTP, got %s", updatedDevice.Info.ProtocolType)
	}
	if updatedDevice.Info.FirmwareVersion != "v1.5.0" {
		t.Errorf("Expected firmware version v1.5.0, got %s", updatedDevice.Info.FirmwareVersion)
	}
}

func TestHandleNextApp_APIKey(t *testing.T) {
	s := newTestServerAPI(t)

	// Update device to require API key
	var device data.Device
	s.DB.First(&device, "id = ?", "testdevice")
	s.DB.Model(&device).Update("require_api_key", true)

	// Add an app so /next returns something
	app := data.App{
		DeviceID:  "testdevice",
		Iname:     "testapp",
		Name:      "Test App",
		UInterval: 10,
		Enabled:   true,
		Pushed:    true,
	}
	s.DB.Create(&app)
	if err := s.savePushedImage("testdevice", "testapp", []byte("dummy image")); err != nil {
		t.Fatalf("Failed to save pushed image: %v", err)
	}

	t.Run("without API key returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/testdevice/next", nil)
		req.SetPathValue("id", "testdevice")

		rr := httptest.NewRecorder()
		s.handleNextApp(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401 Unauthorized, got %v", rr.Code)
		}
	})

	t.Run("with incorrect API key returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/testdevice/next?key=wrongkey", nil)
		req.SetPathValue("id", "testdevice")

		rr := httptest.NewRecorder()
		s.handleNextApp(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401 Unauthorized, got %v", rr.Code)
		}
	})

	t.Run("with correct API key via query param returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/testdevice/next?key=device_api_key", nil)
		req.SetPathValue("id", "testdevice")

		rr := httptest.NewRecorder()
		s.handleNextApp(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected status 200 OK, got %v", rr.Code)
		}
	})

	t.Run("with correct API key via Bearer header returns 200", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/testdevice/next", nil)
		req.SetPathValue("id", "testdevice")
		req.Header.Set("Authorization", "Bearer device_api_key")

		rr := httptest.NewRecorder()
		s.handleNextApp(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected status 200 OK, got %v", rr.Code)
		}
	})
}

func TestHandleWS_APIKey(t *testing.T) {
	s := newTestServerAPI(t)

	// Update device to require API key
	var device data.Device
	s.DB.First(&device, "id = ?", "testdevice")
	s.DB.Model(&device).Update("require_api_key", true)

	t.Run("without API key returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v0/devices/testdevice/ws", nil)
		req.SetPathValue("id", "testdevice")

		rr := httptest.NewRecorder()
		s.handleWS(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401 Unauthorized, got %v", rr.Code)
		}
	})

	t.Run("with incorrect API key returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v0/devices/testdevice/ws?key=wrongkey", nil)
		req.SetPathValue("id", "testdevice")

		rr := httptest.NewRecorder()
		s.handleWS(rr, req)

		if rr.Code != http.StatusUnauthorized {
			t.Errorf("Expected status 401 Unauthorized, got %v", rr.Code)
		}
	})

	t.Run("with correct API key via query param does not return 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v0/devices/testdevice/ws?key=device_api_key", nil)
		req.SetPathValue("id", "testdevice")

		rr := httptest.NewRecorder()
		s.handleWS(rr, req)

		if rr.Code == http.StatusUnauthorized {
			t.Errorf("Expected status not 401 Unauthorized, got %v", rr.Code)
		}
	})

	t.Run("with correct API key via Bearer header does not return 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v0/devices/testdevice/ws", nil)
		req.SetPathValue("id", "testdevice")
		req.Header.Set("Authorization", "Bearer device_api_key")

		rr := httptest.NewRecorder()
		s.handleWS(rr, req)

		if rr.Code == http.StatusUnauthorized {
			t.Errorf("Expected status not 401 Unauthorized, got %v", rr.Code)
		}
	})
}
