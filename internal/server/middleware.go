package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"tronbyt-server/internal/data"
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
		if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&user, "api_key = ?", apiKey).Error; err == nil {
			ctx := context.WithValue(r.Context(), userContextKey, &user)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 2. Try to find Device by API Key
		var device data.Device
		if err := s.DB.First(&device, "api_key = ?", apiKey).Error; err == nil {
			var owner data.User
			if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&owner, "username = ?", device.Username).Error; err != nil {
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, &owner)
			ctx = context.WithValue(ctx, deviceContextKey, &device)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

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
		result := s.DB.Preload("Devices").Preload("Devices.Apps").Limit(1).Find(&user, "username = ?", username)
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

// APIAuth is a helper to wrap APIAuthMiddleware for ServeMux which expects generic handler.
func (s *Server) APIAuth(next http.HandlerFunc) http.HandlerFunc {
	return s.APIAuthMiddleware(next).ServeHTTP
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
