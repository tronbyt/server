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

// isValidAudioContent inspects magic bytes and MIME sniffing to ensure the uploaded file is audio.
func isValidAudioContent(buf []byte, ext string) bool {
	if len(buf) < 4 {
		return false
	}
	mimeType := http.DetectContentType(buf)
	if strings.HasPrefix(mimeType, "audio/") || mimeType == "application/ogg" {
		return true
	}
	// WAV: "RIFF" ... "WAVE"
	if len(buf) >= 12 && string(buf[0:4]) == "RIFF" && string(buf[8:12]) == "WAVE" {
		return true
	}
	// OGG: "OggS"
	if string(buf[0:4]) == "OggS" {
		return true
	}
	// MP3: "ID3" or MPEG audio frame sync (0xFF followed by 0xE0..0xFF)
	if string(buf[0:3]) == "ID3" || (buf[0] == 0xFF && (buf[1]&0xE0) == 0xE0) {
		return true
	}
	// M4A / MP4 audio container: "ftyp" at offset 4
	if len(buf) >= 8 && string(buf[4:8]) == "ftyp" {
		return true
	}
	return false
}

// handleAdminUploadSound processes administrator audio file uploads for notification sounds.
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

	if _, err := os.Stat(targetPath); err == nil {
		s.flashAndRedirect(w, r, fmt.Sprintf("Sound '%s' already exists.", cleanFilename), "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	// Sniff the first 512 bytes to verify actual audio content before writing
	sniffBuf := make([]byte, 512)
	n, readErr := io.ReadFull(file, sniffBuf)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		slog.Error("Failed to read uploaded sound header", "error", readErr)
		s.flashAndRedirect(w, r, "Failed to read uploaded file", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}
	sniffBuf = sniffBuf[:n]
	if !isValidAudioContent(sniffBuf, ext) {
		s.flashAndRedirect(w, r, "Uploaded file does not contain valid audio data", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	dst, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		slog.Error("Failed to create sound file", "path", targetPath, "error", err)
		s.flashAndRedirect(w, r, "Failed to save file", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}
	defer func() { _ = dst.Close() }()

	if _, err := dst.Write(sniffBuf); err != nil {
		_ = dst.Close()
		_ = os.Remove(targetPath)
		slog.Error("Failed to write sound file header", "path", targetPath, "error", err)
		s.flashAndRedirect(w, r, "Failed to save file content", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	if _, err := io.Copy(dst, file); err != nil {
		_ = dst.Close()
		_ = os.Remove(targetPath)
		slog.Error("Failed to write sound file", "path", targetPath, "error", err)
		s.flashAndRedirect(w, r, "Failed to save file content", "/settings/admin#section-sounds", http.StatusSeeOther)
		return
	}

	s.flashAndRedirect(w, r, fmt.Sprintf("Sound '%s' uploaded successfully.", cleanFilename), "/settings/admin#section-sounds", http.StatusSeeOther)
}

// handleAdminDeleteSound removes a custom notification sound file from the sounds directory.
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
