package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
)

func (s *Server) handleAdminSettingsGet(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/settings/admin", http.StatusSeeOther)
}

func (s *Server) handleAdminSettingsPost(w http.ResponseWriter, r *http.Request) {
	// Check admin
	user := GetUser(r)
	if user == nil || !user.IsAdmin {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	localizer := s.getLocalizer(r)

	// Get form values
	oidcEnabled := r.FormValue("oidc_enabled") == "on"
	oidcIssuerURL := r.FormValue("oidc_issuer_url")
	oidcClientID := r.FormValue("oidc_client_id")
	oidcClientSecret := r.FormValue("oidc_client_secret")
	oidcAdditionalScopes := r.FormValue("oidc_additional_scopes")
	oidcAllowAutoCreate := r.FormValue("oidc_allow_auto_create") == "on"
	oidcUsernameClaim := r.FormValue("oidc_username_claim")
	oidcAdminGroupClaim := r.FormValue("oidc_admin_group_claim")
	oidcAdminGroupValue := r.FormValue("oidc_admin_group_value")

	// Apply to config (temporarily for this session)
	s.Config.OIDCEnabled = oidcEnabled
	s.Config.OIDCIssuerURL = oidcIssuerURL
	s.Config.OIDCClientID = oidcClientID
	s.Config.OIDCClientSecret = oidcClientSecret
	s.Config.OIDCAdditionalScopes = oidcAdditionalScopes
	s.Config.OIDCAllowAutoCreate = oidcAllowAutoCreate
	s.Config.OIDCUsernameClaim = oidcUsernameClaim
	s.Config.OIDCAdminGroupClaim = oidcAdminGroupClaim
	s.Config.OIDCAdminGroupValue = oidcAdminGroupValue

	// Save to database settings
	settings := map[string]string{
		"oidc_enabled":           boolToString(oidcEnabled),
		"oidc_issuer_url":        oidcIssuerURL,
		"oidc_client_id":         oidcClientID,
		"oidc_client_secret":     oidcClientSecret,
		"oidc_additional_scopes": oidcAdditionalScopes,
		"oidc_allow_auto_create": boolToString(oidcAllowAutoCreate),
		"oidc_username_claim":    oidcUsernameClaim,
		"oidc_admin_group_claim": oidcAdminGroupClaim,
		"oidc_admin_group_value": oidcAdminGroupValue,
	}

	for key, value := range settings {
		if err := s.setSetting(key, value); err != nil {
			slog.Error("Failed to save setting", "key", key, "error", err)
		}
	}

	session, _ := s.Store.Get(r, "session-name")

	// Reinitialize OIDC provider if enabled
	if oidcEnabled && oidcIssuerURL != "" {
		prov, err := s.setupOIDCProvider(context.Background())
		if err != nil {
			slog.Warn("Failed to reinitialize OIDC provider", "error", err)
			session.AddFlash("Failed to initialize OIDC provider: " + err.Error())
			if err := s.saveSession(w, r, session); err != nil {
				slog.Error("Failed to save session after OIDC provider error", "error", err)
			}
			http.Redirect(w, r, "/settings/admin", http.StatusSeeOther)
			return
		}
		s.OIDCProvider = prov
	} else {
		s.OIDCProvider = nil
	}

	session.AddFlash(localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDC settings saved."}))
	if err := s.saveSession(w, r, session); err != nil {
		slog.Error("Failed to save session after settings update", "error", err)
	}
	http.Redirect(w, r, "/settings/admin", http.StatusSeeOther)
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (s *Server) handleAdminUploadSound(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	// Limit upload size to 10MB and cap body stream to prevent unbounded disk spooling
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		s.flashAndRedirect(w, r, "File too large (max 10MB)", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	file, header, err := r.FormFile("sound_file")
	if err != nil {
		s.flashAndRedirect(w, r, "No file provided", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}
	defer func() { _ = file.Close() }()

	ext := strings.ToLower(filepath.Ext(header.Filename))
	if !slices.Contains(allowedAudioExtensions, ext) {
		s.flashAndRedirect(w, r, fmt.Sprintf("Invalid audio format %s. Allowed: %s", ext, strings.Join(allowedAudioExtensions, ", ")), "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	cleanFilename := filepath.Base(strings.TrimSpace(header.Filename))
	if cleanFilename == "" || cleanFilename == "." {
		s.flashAndRedirect(w, r, "Invalid filename", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	if err := s.EnsureSoundsDir(); err != nil {
		slog.Error("Failed to ensure sounds directory", "error", err)
		s.flashAndRedirect(w, r, "Failed to create sounds directory", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	targetPath, err := securejoin.SecureJoin(s.SoundsDir(), cleanFilename)
	if err != nil {
		slog.Warn("Path traversal attempt in sound upload", "filename", header.Filename, "error", err)
		s.flashAndRedirect(w, r, "Invalid filename", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	dst, err := os.Create(targetPath)
	if err != nil {
		slog.Error("Failed to create sound file", "path", targetPath, "error", err)
		s.flashAndRedirect(w, r, "Failed to save file", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}
	defer func() { _ = dst.Close() }()

	if _, err := io.Copy(dst, file); err != nil {
		slog.Error("Failed to write sound file", "path", targetPath, "error", err)
		s.flashAndRedirect(w, r, "Failed to save file content", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	s.flashAndRedirect(w, r, fmt.Sprintf("Sound '%s' uploaded successfully.", cleanFilename), "/settings/admin#section-sounds", http.StatusSeeOther)
}

func (s *Server) handleAdminDeleteSound(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	if user == nil || !user.IsAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	filename := filepath.Base(strings.TrimSpace(r.PathValue("filename")))
	if filename == "" || filename == "." {
		s.flashAndRedirect(w, r, "Invalid filename", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}
	targetPath, err := securejoin.SecureJoin(s.SoundsDir(), filename)
	if err != nil {
		slog.Warn("Path traversal attempt in sound delete", "filename", filename, "error", err)
		s.flashAndRedirect(w, r, "Invalid filename", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
		slog.Error("Failed to delete sound file", "path", targetPath, "error", err)
		s.flashAndRedirect(w, r, "Failed to delete file", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	s.flashAndRedirect(w, r, fmt.Sprintf("Sound '%s' deleted successfully.", filename), "/settings/admin#section-sounds", http.StatusSeeOther)
}
