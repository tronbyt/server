package server

import (
	"context"
	"log/slog"
	"net/http"

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
	oidcAllowAutoCreate := r.FormValue("oidc_allow_auto_create") == "on"
	oidcUsernameClaim := r.FormValue("oidc_username_claim")
	oidcAdminGroupClaim := r.FormValue("oidc_admin_group_claim")
	oidcAdminGroupValue := r.FormValue("oidc_admin_group_value")

	// Apply to config (temporarily for this session)
	s.Config.OIDCEnabled = oidcEnabled
	s.Config.OIDCIssuerURL = oidcIssuerURL
	s.Config.OIDCClientID = oidcClientID
	s.Config.OIDCClientSecret = oidcClientSecret
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
			s.saveSession(w, r, session)
			http.Redirect(w, r, "/settings/admin", http.StatusSeeOther)
			return
		}
		s.OIDCProvider = prov
	} else {
		s.OIDCProvider = nil
	}

	session.AddFlash(localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDC settings saved."}))
	s.saveSession(w, r, session)
	http.Redirect(w, r, "/settings/admin", http.StatusSeeOther)
}

func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
