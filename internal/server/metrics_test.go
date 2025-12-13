package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestMetricsEndpoint(t *testing.T) {
	// Setup ephemeral DB
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}
	if err := db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.Setting{}); err != nil {
		t.Fatalf("Failed to migrate DB: %v", err)
	}

	// Setup Server
	cfg := &config.Settings{
		DataDir: t.TempDir(),
	}
	s := NewServer(db, cfg)

	// Create Request
	req, err := http.NewRequest(http.MethodGet, "/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()

	// Serve Request
	s.ServeHTTP(rr, req)

	// Check Status Code
	assert.Equal(t, http.StatusOK, rr.Code, "handler returned wrong status code")

	// Check Body contains standard Go metrics
	body := rr.Body.String()
	assert.True(t, strings.Contains(body, "go_info"), "body does not contain go_info metric")
	assert.True(t, strings.Contains(body, "go_goroutines"), "body does not contain go_goroutines metric")
}
