package server

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"tronbyt-server/internal/data"
)

type contextKey string

const (
	userContextKey   contextKey = "user"
	deviceContextKey contextKey = "device"
)

// APIAuthMiddleware authenticates requests using the Authorization header (API Key).
// It mimics `get_user_and_device_from_api_key` from the Python codebase.
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
			// User found. If the path contains a device ID, try to find it in the user's devices.
			// Path value parsing depends on Go 1.22+ routing match.
			// However, middleware runs *before* strict path matching in some cases or wraps the handler.
			// Let's assume we can parse it from URL or the final handler validates the device ownership.
			// Storing User in context.
			ctx := context.WithValue(r.Context(), userContextKey, &user)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// 2. Try to find Device by API Key
		var device data.Device
		if err := s.DB.First(&device, "api_key = ?", apiKey).Error; err == nil {
			// Device found. Find the owner.
			var owner data.User
			if err := s.DB.Preload("Devices").Preload("Devices.Apps").First(&owner, "username = ?", device.Username).Error; err != nil {
				// Should not happen if DB is consistent
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				return
			}

			// If the URL has a device ID, verify it matches or is owned by the user?
			// The Python code: "if device and device.api_key == api_key: return user, device"
			// It implies this API key grants access to THIS specific device.

			// We store both User and Device in context.
			ctx := context.WithValue(r.Context(), userContextKey, &owner)
			ctx = context.WithValue(ctx, deviceContextKey, &device)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}

// UserFromContext retrieves the User from the context
func UserFromContext(ctx context.Context) (*data.User, error) {
	u, ok := ctx.Value(userContextKey).(*data.User)
	if !ok {
		return nil, errors.New("user not found in context")
	}
	return u, nil
}

// DeviceFromContext retrieves the Device from the context (if authenticated via Device Key)
func DeviceFromContext(ctx context.Context) (*data.Device, error) {
	d, ok := ctx.Value(deviceContextKey).(*data.Device)
	if !ok {
		return nil, errors.New("device not found in context")
	}
	return d, nil
}
