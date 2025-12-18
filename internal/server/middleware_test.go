package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGzipMiddleware(t *testing.T) {
	tests := []struct {
		name                  string
		acceptEncoding        string
		contentType           string
		contentEncoding       string
		responseBody          string
		expectGzip            bool
		expectContentEncoding string
	}{
		{
			name:                  "No Accept-Encoding",
			acceptEncoding:        "",
			contentType:           "text/plain",
			responseBody:          "Hello World",
			expectGzip:            false,
			expectContentEncoding: "",
		},
		{
			name:                  "Accept-Encoding gzip, text content",
			acceptEncoding:        "gzip",
			contentType:           "text/plain",
			responseBody:          "Hello World",
			expectGzip:            true,
			expectContentEncoding: "gzip",
		},
		{
			name:                  "Accept-Encoding gzip, text content with charset",
			acceptEncoding:        "gzip",
			contentType:           "text/plain; charset=utf-8",
			responseBody:          "Hello World",
			expectGzip:            true,
			expectContentEncoding: "gzip",
		},
		{
			name:                  "Accept-Encoding gzip, JSON content",
			acceptEncoding:        "gzip",
			contentType:           "application/json",
			responseBody:          `{"hello": "world"}`,
			expectGzip:            true,
			expectContentEncoding: "gzip",
		},
		{
			name:                  "Accept-Encoding gzip, SVG content",
			acceptEncoding:        "gzip",
			contentType:           "image/svg+xml",
			responseBody:          `<svg></svg>`,
			expectGzip:            true,
			expectContentEncoding: "gzip",
		},
		{
			name:                  "Accept-Encoding gzip, image content (not in allowlist)",
			acceptEncoding:        "gzip",
			contentType:           "image/png",
			responseBody:          "fake png data",
			expectGzip:            false,
			expectContentEncoding: "",
		},
		{
			name:                  "Accept-Encoding gzip, font content (not in allowlist)",
			acceptEncoding:        "gzip",
			contentType:           "font/woff2",
			responseBody:          "fake woff2 data",
			expectGzip:            false,
			expectContentEncoding: "",
		},
		{
			name:                  "Accept-Encoding gzip, PDF content (not in allowlist)",
			acceptEncoding:        "gzip",
			contentType:           "application/pdf",
			responseBody:          "fake pdf data",
			expectGzip:            false,
			expectContentEncoding: "",
		},
		{
			name:                  "Accept-Encoding gzip, already compressed (Content-Encoding set)",
			acceptEncoding:        "gzip",
			contentType:           "text/plain",
			contentEncoding:       "br", // handler set brotli
			responseBody:          "fake brotli data",
			expectGzip:            false,
			expectContentEncoding: "br",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				if tt.contentEncoding != "" {
					w.Header().Set("Content-Encoding", tt.contentEncoding)
				}
				_, _ = w.Write([]byte(tt.responseBody))
			}))

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tt.acceptEncoding)
			}
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Header().Get("Content-Encoding") != tt.expectContentEncoding {
				t.Errorf("expected Content-Encoding %q, got %q", tt.expectContentEncoding, rr.Header().Get("Content-Encoding"))
			}

			if tt.expectGzip {
				// Verify body is gzipped (magic bytes)
				if !bytes.HasPrefix(rr.Body.Bytes(), []byte{0x1f, 0x8b}) {
					t.Error("expected gzipped body, got plain text")
				}
			} else {
				if rr.Body.String() != tt.responseBody {
					t.Errorf("expected body %q, got %q", tt.responseBody, rr.Body.String())
				}
			}
		})
	}

	// Additional cases for status codes and headers that require custom handler logic
	t.Run("with 204 No Content", func(t *testing.T) {
		handler := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

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
		handler := GzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("Hello"))
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		rr := httptest.NewRecorder()

		handler.ServeHTTP(rr, req)

		if rr.Header().Get("Content-Encoding") != "" {
			t.Errorf("Expected no Content-Encoding for WS upgrade, got %v", rr.Header().Get("Content-Encoding"))
		}
		if rr.Body.String() != "Hello" {
			t.Errorf("Expected plain body 'Hello', got %q", rr.Body.String())
		}
	})
}
