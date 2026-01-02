package server

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"tronbyt-server/internal/data"

	"gorm.io/gorm"
)

type contextKey string

const (
	userContextKey   contextKey = "user"
	deviceContextKey contextKey = "device"
	appContextKey    contextKey = "app"
)

// APIAuthMiddleware authenticates requests using the Authorization header (API Key).
func (s *Server) APIAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		parts := strings.Split(authHeader, " ")
		var apiKey string
		if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
			apiKey = parts[1]
		} else {
			apiKey = authHeader
		}

		if apiKey == "" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// 1. Try to find User by API Key
		var user data.User
		userResult := s.DB.Preload("Devices", func(db *gorm.DB) *gorm.DB {
			return db.Order("name ASC")
		}).Preload("Devices.Apps").Limit(1).Find(&user, "api_key = ?", apiKey)

		if userResult.Error != nil {
			slog.Error("API auth: database error when finding user by key", "error", userResult.Error)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if userResult.RowsAffected > 0 {
			ctx := context.WithValue(r.Context(), userContextKey, &user)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 2. Try to find Device by API Key
		var device data.Device
		deviceResult := s.DB.Preload("Apps").Limit(1).Find(&device, "api_key = ?", apiKey)
		if deviceResult.Error != nil {
			slog.Error("API auth: database error when finding device by key", "error", deviceResult.Error)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		if deviceResult.RowsAffected > 0 {
			var owner data.User
			if err := s.DB.Preload("Devices", func(db *gorm.DB) *gorm.DB {
				return db.Order("name ASC")
			}).Preload("Devices.Apps").First(&owner, "username = ?", device.Username).Error; err != nil {
				slog.Error("API auth: database error finding device owner", "username", device.Username, "error", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, &owner)
			ctx = context.WithValue(ctx, deviceContextKey, &device)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		slog.Info("API authentication failed")
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

// RequireLogin authenticates Web UI requests via session cookie.
func (s *Server) RequireLogin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, _ := s.Store.Get(r, "session-name")
		username, ok := session.Values["username"].(string)
		if !ok {
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}

		var user data.User
		// Preload everything we might need
		// Use Limit(1).Find to avoid GORM "record not found" error log for stale sessions
		result := s.DB.Preload("Devices", func(db *gorm.DB) *gorm.DB {
			return db.Order("name ASC")
		}).Preload("Devices.Apps").Limit(1).Find(&user, "username = ?", username)
		if result.Error != nil {
			slog.Error("Database error checking session user", "username", username, "error", result.Error)
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}

		if result.RowsAffected == 0 {
			slog.Info("User in session not found in DB", "username", username)
			http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
			return
		}

		ctx := context.WithValue(r.Context(), userContextKey, &user)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// RequireDevice ensures a device ID is present and owned by the authenticated user.
// Must be used after RequireLogin or APIAuthMiddleware.
func (s *Server) RequireDevice(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			http.Error(w, "Device ID required", http.StatusBadRequest)
			return
		}

		user, err := UserFromContext(r.Context())
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Check if device is already loaded (e.g. by API key) and matches
		if d, err := DeviceFromContext(r.Context()); err == nil {
			if d.ID == id {
				next.ServeHTTP(w, r)
				return
			}
			// If authorized via Device Key X, but asking for Device Y, deny.
			http.Error(w, "Forbidden: Device Key mismatch", http.StatusForbidden)
			return
		}

		// Look for device in user's devices
		var device *data.Device
		for i := range user.Devices {
			if user.Devices[i].ID == id {
				device = &user.Devices[i]
				break
			}
		}

		if device == nil {
			http.Error(w, "Device not found", http.StatusNotFound)
			return
		}

		ctx := context.WithValue(r.Context(), deviceContextKey, device)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// RequireApp ensures an app iname is present and belongs to the context device.
// Must be used after RequireDevice.
func (s *Server) RequireApp(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		iname := r.PathValue("iname")
		if iname == "" {
			http.Error(w, "App ID (iname) required", http.StatusBadRequest)
			return
		}

		device, err := DeviceFromContext(r.Context())
		if err != nil {
			http.Error(w, "Device context missing", http.StatusInternalServerError)
			return
		}

		var app *data.App
		for i := range device.Apps {
			if device.Apps[i].Iname == iname {
				app = &device.Apps[i]
				break
			}
		}

		if app == nil {
			http.Error(w, "App not found", http.StatusNotFound)
			return
		}

		ctx := context.WithValue(r.Context(), appContextKey, app)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// UserFromContext retrieves the User from the context.
func UserFromContext(ctx context.Context) (*data.User, error) {
	u, ok := ctx.Value(userContextKey).(*data.User)
	if !ok {
		return nil, errors.New("user not found in context")
	}
	return u, nil
}

// DeviceFromContext retrieves the Device from the context.
func DeviceFromContext(ctx context.Context) (*data.Device, error) {
	d, ok := ctx.Value(deviceContextKey).(*data.Device)
	if !ok {
		return nil, errors.New("device not found in context")
	}
	return d, nil
}

// AppFromContext retrieves the App from the context.
func AppFromContext(ctx context.Context) (*data.App, error) {
	a, ok := ctx.Value(appContextKey).(*data.App)
	if !ok {
		return nil, errors.New("app not found in context")
	}
	return a, nil
}

// GetUser is a helper to get Context objects without error checking (panics if missing, use only within middleware).
func GetUser(r *http.Request) *data.User {
	u, _ := UserFromContext(r.Context())
	if u == nil {
		panic("GetUser called without RequireLogin/AuthMiddleware")
	}
	return u
}

func GetDevice(r *http.Request) *data.Device {
	d, _ := DeviceFromContext(r.Context())
	if d == nil {
		panic("GetDevice called without RequireDevice middleware")
	}
	return d
}

func GetApp(r *http.Request) *data.App {
	a, _ := AppFromContext(r.Context())
	if a == nil {
		panic("GetApp called without RequireApp middleware")
	}
	return a
}

func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		slog.Debug("Request started", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
		slog.Debug("Request finished", "method", r.Method, "path", r.URL.Path, "duration", time.Since(start))
	})
}

func ProxyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			r.URL.Scheme = proto
		}
		if host := r.Header.Get("X-Forwarded-Host"); host != "" {
			r.Host = host
			r.URL.Host = host
		}
		next.ServeHTTP(w, r)
	})
}

func RecoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("Panic recovered", "error", err, "stack", string(debug.Stack()))
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

var gzipWriterPool = sync.Pool{
	New: func() any {
		return gzip.NewWriter(io.Discard)
	},
}

// GzipMiddleware compresses HTTP responses with gzip if the client supports it.
func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip") ||
			strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
			next.ServeHTTP(w, r)
			return
		}

		gzw := &gzipResponseWriter{ResponseWriter: w}
		defer func() {
			if r := recover(); r != nil {
				// We are panicking. Do not close the gzip writer to avoid writing a footer
				// which would corrupt the response if the recover middleware writes an error.
				// We must re-panic to allow the recovery middleware to handle it.
				panic(r)
			}
			if err := gzw.Close(); err != nil {
				slog.Error("Failed to close gzip writer", "error", err)
			}
		}()

		next.ServeHTTP(gzw, r)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter

	gz          *gzip.Writer
	wroteHeader bool
	hijacked    bool
	skip        bool
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true

	// If Content-Encoding is already set, skip compression
	if w.Header().Get("Content-Encoding") != "" {
		w.skip = true
	}

	// Check Content-Type against allowlist
	contentType := w.Header().Get("Content-Type")
	// Clean up content type (remove params like charset)
	if idx := strings.Index(contentType, ";"); idx != -1 {
		contentType = contentType[:idx]
	}
	contentType = strings.TrimSpace(strings.ToLower(contentType))

	isCompressible := strings.HasPrefix(contentType, "text/") ||
		contentType == "application/json" ||
		contentType == "application/javascript" ||
		contentType == "application/x-javascript" ||
		contentType == "application/xml" ||
		contentType == "application/ld+json" ||
		contentType == "image/svg+xml"

	if !isCompressible {
		w.skip = true
	}

	// If response code does not allow body, do not create gzip writer
	if code == http.StatusNoContent || code == http.StatusNotModified || (code >= 100 && code < 200) {
		w.skip = true
	}

	if !w.skip {
		w.Header().Del("Content-Length")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
	}
	w.ResponseWriter.WriteHeader(code)

	if !w.skip {
		w.gz = gzipWriterPool.Get().(*gzip.Writer)
		w.gz.Reset(w.ResponseWriter)
	}
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		// Detect content type if not set
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", http.DetectContentType(b))
		}
		w.WriteHeader(http.StatusOK)
	}
	if w.skip {
		return w.ResponseWriter.Write(b)
	}

	// CRITICAL: If compression is not skipped, w.gz *must* be initialized.
	// If it's nil, it indicates an internal server error or unexpected state,
	// and writing uncompressed data would lead to client-side decompression failures.
	if w.gz == nil {
		slog.Error("gzip writer is nil when compression is not skipped, but Content-Encoding: gzip header was set.")
		return 0, errors.New("internal server error: failed to write gzipped response")
	}

	return w.gz.Write(b)
}

func (w *gzipResponseWriter) Close() error {
	if w.hijacked || w.skip {
		return nil
	}
	if w.gz != nil {
		err := w.gz.Close()
		gzipWriterPool.Put(w.gz)
		w.gz = nil
		return err
	}
	return nil
}

func (w *gzipResponseWriter) Flush() {
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Hijack implements http.Hijacker.
func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("http.Hijacker not supported by underlying ResponseWriter")
	}
	w.hijacked = true // Mark as hijacked
	return h.Hijack()
}

// Push implements http.Pusher.
func (w *gzipResponseWriter) Push(target string, opts *http.PushOptions) error {
	p, ok := w.ResponseWriter.(http.Pusher)
	if !ok {
		return errors.New("http.Pusher not supported by underlying ResponseWriter")
	}
	return p.Push(target, opts)
}
