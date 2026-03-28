package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMetricsEndpoint(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=private"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}
	if err := db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.Setting{}); err != nil {
		t.Fatalf("Failed to migrate DB: %v", err)
	}

	cfg := &config.Settings{
		DataDir:    t.TempDir(),
		Production: false,
	}
	s := NewServer(db, cfg)

	// Switch to an isolated registry so this test doesn't conflict with others.
	reg := prometheus.NewRegistry()
	s.PromRegistry = reg
	s.PromGatherer = reg
	s.registerMetrics()

	req, err := http.NewRequest(http.MethodGet, "/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	body := rr.Body.String()

	// Application-specific metrics
	assert.Contains(t, body, "tronbyt_renders_total")
	assert.Contains(t, body, "tronbyt_render_duration_seconds")
	assert.Contains(t, body, "tronbyt_device_polls_total")
	assert.Contains(t, body, "tronbyt_websocket_connections")
	assert.Contains(t, body, "tronbyt_http_request_duration_seconds")
	assert.Contains(t, body, "tronbyt_users")
	assert.Contains(t, body, "tronbyt_devices")
	assert.Contains(t, body, "tronbyt_devices_active")
	assert.Contains(t, body, "tronbyt_apps")

	// Second scrape should show http_requests_total (CounterVec appears after first observation).
	rr2 := httptest.NewRecorder()
	s.ServeHTTP(rr2, req)
	assert.Contains(t, rr2.Body.String(), "tronbyt_http_requests_total")
}
