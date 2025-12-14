package server

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"tronbyt-server/internal/config"
	"tronbyt-server/internal/data"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newTestServer(t *testing.T) *Server {
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open DB: %v", err)
	}

	if err := db.AutoMigrate(&data.User{}, &data.Device{}, &data.App{}, &data.WebAuthnCredential{}, &data.Setting{}); err != nil {
		t.Fatalf("Failed to migrate DB: %v", err)
	}

	// Pre-seed settings to avoid "record not found" logs during NewServer
	db.Create(&data.Setting{Key: "secret_key", Value: "testsecret"})
	db.Create(&data.Setting{Key: "system_apps_repo", Value: ""})

	cfg := &config.Settings{}

	s := NewServer(db, cfg)
	s.DataDir = t.TempDir()
	return s
}

func TestLoginRedirectToRegisterIfNoUsers(t *testing.T) {
	s := newTestServer(t)

	// Ensure no users
	var count int64
	s.DB.Model(&data.User{}).Count(&count)
	if count != 0 {
		t.Fatalf("Expected 0 users, got %d", count)
	}

	req, _ := http.NewRequest(http.MethodGet, "/auth/login", nil)
	rr := httptest.NewRecorder()

	s.ServeHTTP(rr, req)

	if rr.Code != http.StatusSeeOther {
		t.Errorf("handler returned wrong status code: got %v want %v",
			rr.Code, http.StatusSeeOther)
	}

	location, _ := rr.Result().Location()
	if location.Path != "/auth/register" {
		t.Errorf("handler returned wrong redirect location: got %v want %v",
			location.Path, "/auth/register")
	}
}

func TestGzipMiddleware(t *testing.T) {
	s := newTestServer(t)
	s.Router.HandleFunc("GET /gzip-test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if _, err := w.Write([]byte("Hello, Gzip!")); err != nil {
			t.Logf("Write failed: %v", err)
		}
	})
	s.Router.HandleFunc("GET /gzip-204", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	t.Run("with Accept-Encoding gzip", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/gzip-test", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()

		s.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected 200 OK, got %v", rr.Code)
		}

		if rr.Header().Get("Content-Encoding") != "gzip" {
			t.Errorf("Expected Content-Encoding: gzip, got %v", rr.Header().Get("Content-Encoding"))
		}

		// Verify content
		gr, err := gzip.NewReader(rr.Body)
		if err != nil {
			t.Fatalf("Failed to create gzip reader: %v", err)
		}
		defer func() { _ = gr.Close() }()

		body, err := io.ReadAll(gr)
		if err != nil {
			t.Fatalf("Failed to read gzip body: %v", err)
		}

		if string(body) != "Hello, Gzip!" {
			t.Errorf("Expected body 'Hello, Gzip!', got %q", string(body))
		}
	})

	t.Run("without Accept-Encoding", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/gzip-test", nil)
		rr := httptest.NewRecorder()

		s.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected 200 OK, got %v", rr.Code)
		}

		if rr.Header().Get("Content-Encoding") != "" {
			t.Errorf("Expected no Content-Encoding, got %v", rr.Header().Get("Content-Encoding"))
		}

		if rr.Body.String() != "Hello, Gzip!" {
			t.Errorf("Expected body 'Hello, Gzip!', got %q", rr.Body.String())
		}
	})

	t.Run("with 204 No Content", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/gzip-204", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()

		s.ServeHTTP(rr, req)

		if rr.Code != http.StatusNoContent {
			t.Errorf("Expected 204 No Content, got %v", rr.Code)
		}

		if rr.Header().Get("Content-Encoding") == "gzip" {
			t.Error("Expected no Content-Encoding for 204 response")
		}

		if rr.Body.Len() > 0 {
			t.Errorf("Expected empty body for 204, got length %d", rr.Body.Len())
		}
	})

	t.Run("WebSocket Upgrade Skipped", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/gzip-test", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		rr := httptest.NewRecorder()

		s.ServeHTTP(rr, req)

		if rr.Code != http.StatusOK {
			t.Errorf("Expected 200 OK (mock handler), got %v", rr.Code)
		}

		if rr.Header().Get("Content-Encoding") != "" {
			t.Errorf("Expected no Content-Encoding for WS upgrade, got %v", rr.Header().Get("Content-Encoding"))
		}
	})
}
