package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"tronbyt-server/internal/data"
)

// OIDCProvider holds the OIDC provider and verifier.
type OIDCProvider struct {
	provider     *oidc.Provider
	verifier     *oidc.IDTokenVerifier
	clientID     string
	clientSecret string
	issuerURL    string
}

func (s *Server) setupOIDCProvider(ctx context.Context) (*OIDCProvider, error) {
	if !s.Config.OIDCEnabled || s.Config.OIDCIssuerURL == "" {
		return nil, nil
	}

	provider, err := oidc.NewProvider(ctx, s.Config.OIDCIssuerURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create OIDC provider: %w", err)
	}

	verifier := provider.Verifier(&oidc.Config{
		ClientID: s.Config.OIDCClientID,
	})

	return &OIDCProvider{
		provider:     provider,
		verifier:     verifier,
		clientID:     s.Config.OIDCClientID,
		clientSecret: s.Config.OIDCClientSecret,
		issuerURL:    s.Config.OIDCIssuerURL,
	}, nil
}

// oauth2Config returns the OAuth2 configuration with a dynamic redirect URI.
func (p *OIDCProvider) oauth2Config(baseURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.clientID,
		ClientSecret: p.clientSecret,
		RedirectURL:  baseURL + "/auth/oidc/callback",
		Endpoint:     p.provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if !s.Config.OIDCEnabled {
		http.Error(w, "OIDC is not enabled", http.StatusForbidden)
		return
	}

	prov := s.OIDCProvider
	if prov == nil {
		slog.Error("OIDC provider not initialized")
		http.Error(w, "OIDC provider not configured", http.StatusInternalServerError)
		return
	}

	// Generate secure state and nonce
	state, err := generateSecureTokenEncoded(32)
	if err != nil {
		slog.Error("Failed to generate secure state", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	nonce, err := generateSecureTokenEncoded(32)
	if err != nil {
		slog.Error("Failed to generate secure nonce", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	// Store in session
	session, _ := s.Store.Get(r, "session-name")
	session.Values["oidc_state"] = state
	session.Values["oidc_nonce"] = nonce
	if err := s.saveSession(w, r, session); err != nil {
		slog.Error("Failed to save session", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	// Build auth URL using oauth2 lib and dynamic redirect URI from GetBaseURL
	oauth2Cfg := prov.oauth2Config(s.GetBaseURL(r))
	authURL := oauth2Cfg.AuthCodeURL(state, oauth2.AccessTypeOffline)

	slog.Debug("Redirecting to OIDC provider", "url", authURL)
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	localizer := s.getLocalizer(r)

	// Check for error from provider
	if errMsg := r.URL.Query().Get("error"); errMsg != "" {
		slog.Warn("OIDC provider returned error", "error", errMsg, "desc", r.URL.Query().Get("error_description"))
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorAuth"})},
		})
		return
	}

	// Get session and validate state
	session, _ := s.Store.Get(r, "session-name")
	expectedState, ok := session.Values["oidc_state"].(string)
	if !ok || expectedState == "" {
		slog.Warn("OIDC callback missing state")
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorAuth"})},
		})
		return
	}

	actualState := r.URL.Query().Get("state")
	if actualState != expectedState {
		slog.Warn("OIDC state mismatch", "expected", expectedState, "actual", actualState)
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorAuth"})},
		})
		return
	}

	expectedNonce, ok := session.Values["oidc_nonce"].(string)
	if !ok {
		expectedNonce = ""
	}

	// Clear state/nonce from session
	delete(session.Values, "oidc_state")
	delete(session.Values, "oidc_nonce")

	// Get code
	code := r.URL.Query().Get("code")
	if code == "" {
		slog.Warn("OIDC callback missing code")
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorAuth"})},
		})
		return
	}

	// Exchange code for tokens
	prov := s.OIDCProvider
	if prov == nil {
		slog.Error("OIDC provider not available")
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorConfig"})},
		})
		return
	}

	oauth2Cfg := prov.oauth2Config(s.GetBaseURL(r))
	token, err := oauth2Cfg.Exchange(ctx, code)
	if err != nil {
		slog.Error("Failed to exchange code for tokens", "error", err)
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorAuth"})},
		})
		return
	}

	// Extract and verify ID token
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		slog.Error("No id_token in response")
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorAuth"})},
		})
		return
	}

	idToken, err := prov.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		slog.Error("Failed to verify ID token", "error", err)
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorAuth"})},
		})
		return
	}

	// Verify nonce
	if idToken.Nonce != expectedNonce {
		slog.Warn("Nonce mismatch")
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorAuth"})},
		})
		return
	}

	// Extract claims dynamically
	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		slog.Error("Failed to parse claims", "error", err)
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorAuth"})},
		})
		return
	}

	// Get username and email from claims
	username := s.extractClaimString(claims, s.Config.OIDCUsernameClaim, "preferred_username")
	if username == "" {
		username = s.extractClaimString(claims, "email", "email")
	}
	if username == "" {
		username = s.extractClaimString(claims, "sub", "sub")
	}

	email := s.extractClaimString(claims, "email", "email")

	// Look up existing identity using GORM Generics API
	identity, err := gorm.G[data.OIDCIdentity](s.DB).Where("subject = ? AND issuer = ?", claims["sub"], prov.issuerURL).First(ctx)
	if err == nil {
		// Found identity, get user
		user, err := gorm.G[data.User](s.DB).Where("username = ?", identity.UserID).First(ctx)
		if err != nil {
			slog.Error("OIDC identity exists but user not found", "userID", identity.UserID)
			s.renderTemplate(w, r, "login", TemplateData{
				Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorNoAccount"})},
			})
			return
		}
		s.handleOIDCSuccess(w, r, &user, claims, &identity, localizer)
		return
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		// Try to find user by username for auto-linking
		user, err := gorm.G[data.User](s.DB).Where("username = ?", username).First(ctx)
		if err == nil {
			// Auto-link existing user
			slog.Info("OIDC login: auto-linking existing user", "username", username)
			s.handleOIDCNewIdentity(w, r, &user, email, claims, prov, localizer)
			return
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			if !s.Config.OIDCAllowAutoCreate {
				slog.Info("OIDC login: no matching user, auto-create disabled", "subject", claims["sub"])
				s.renderTemplate(w, r, "login", TemplateData{
					Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorNoAccount"})},
				})
				return
			}
			// Create new user
			s.handleOIDCCreateUser(w, r, username, email, claims, prov, localizer)
			return
		} else {
			slog.Error("Failed to look up user", "error", err)
			s.renderTemplate(w, r, "login", TemplateData{
				Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorConfig"})},
			})
			return
		}
	} else {
		slog.Error("Failed to look up OIDC identity", "error", err)
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorConfig"})},
		})
		return
	}
}

// extractClaimString extracts a string value from claims map with a fallback.
func (s *Server) extractClaimString(claims map[string]any, key, fallback string) string {
	if key != "" {
		if val, ok := claims[key].(string); ok {
			return val
		}
	}
	if val, ok := claims[fallback].(string); ok {
		return val
	}
	return ""
}

// handleOIDCNewIdentity creates an OIDC identity for existing user and logs them in.
func (s *Server) handleOIDCNewIdentity(w http.ResponseWriter, r *http.Request, user *data.User, email string, claims map[string]any, prov *OIDCProvider, localizer *i18n.Localizer) {
	ctx := r.Context()
	identity := data.OIDCIdentity{
		UserID:  user.Username,
		Subject: claims["sub"].(string),
		Issuer:  prov.issuerURL,
		Email:   &email,
	}
	if err := gorm.G[data.OIDCIdentity](s.DB).Create(ctx, &identity); err != nil {
		slog.Error("Failed to create OIDC identity", "error", err)
	}
	s.handleOIDCSuccess(w, r, user, claims, &identity, localizer)
}

// handleOIDCCreateUser creates a new user from OIDC and logs them in.
func (s *Server) handleOIDCCreateUser(w http.ResponseWriter, r *http.Request, username, email string, claims map[string]any, prov *OIDCProvider, localizer *i18n.Localizer) {
	ctx := r.Context()
	apiKey, err := generateSecureToken(32)
	if err != nil {
		slog.Error("Failed to generate API key for new user", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	var emailPtr *string
	if email != "" {
		emailPtr = &email
	}
	newUser := data.User{
		Username: username,
		Password: "", // No password for OIDC-only users
		Email:    emailPtr,
		APIKey:   apiKey,
		IsAdmin:  false,
	}
	if err := gorm.G[data.User](s.DB).Create(ctx, &newUser); err != nil {
		slog.Error("Failed to create user from OIDC", "error", err)
		s.renderTemplate(w, r, "login", TemplateData{
			Flashes: []string{localizer.MustLocalize(&i18n.LocalizeConfig{MessageID: "OIDCErrorConfig"})},
		})
		return
	}
	slog.Info("OIDC login: created new user", "username", username)

	// Create identity
	identity := data.OIDCIdentity{
		UserID:  newUser.Username,
		Subject: claims["sub"].(string),
		Issuer:  prov.issuerURL,
		Email:   emailPtr,
	}
	if err := gorm.G[data.OIDCIdentity](s.DB).Create(ctx, &identity); err != nil {
		slog.Error("Failed to create OIDC identity", "error", err)
	}

	s.handleOIDCSuccess(w, r, &newUser, claims, &identity, localizer)
}

// handleOIDCSuccess handles successful OIDC login.
func (s *Server) handleOIDCSuccess(w http.ResponseWriter, r *http.Request, user *data.User, claims map[string]any, identity *data.OIDCIdentity, localizer *i18n.Localizer) {
	ctx := r.Context()
	slog.Info("OIDC login: successful", "username", user.Username)

	// Extract email and group claims dynamically
	email := s.extractClaimString(claims, "email", "")
	if email != "" {
		emailChanged := false
		if user.Email == nil || *user.Email != email {
			user.Email = &email
			emailChanged = true
			if _, err := gorm.G[data.User](s.DB).Where("username = ?", user.Username).Update(ctx, "email", &email); err != nil {
				slog.Error("Failed to update user email", "error", err)
			}
		}
		if identity != nil && (identity.Email == nil || *identity.Email != email) {
			identity.Email = &email
			if _, err := gorm.G[data.OIDCIdentity](s.DB).Where("id = ?", identity.ID).Update(ctx, "email", &email); err != nil {
				slog.Error("Failed to update identity email", "error", err)
			}
		}
		if emailChanged {
			slog.Info("OIDC login: email updated", "username", user.Username)
		}
	}

	// Check and update admin status based on groups
	s.checkAndUpdateAdminStatus(ctx, user, claims)

	// Save session
	session, _ := s.Store.Get(r, "session-name")
	session.Values["username"] = user.Username
	session.Options.MaxAge = 86400 * 30
	if err := s.saveSession(w, r, session); err != nil {
		slog.Error("Failed to save session", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// checkAndUpdateAdminStatus checks OIDC groups and updates user admin status.
func (s *Server) checkAndUpdateAdminStatus(ctx context.Context, user *data.User, claims map[string]any) {
	adminValue := s.Config.OIDCAdminGroupValue
	if s.Config.OIDCAdminGroupClaim != "" && adminValue != "" {
		groups := s.extractGroupsFromClaims(claims, s.Config.OIDCAdminGroupClaim)
		isAdmin := slices.Contains(groups, adminValue)
		if user.IsAdmin != isAdmin {
			if _, err := gorm.G[data.User](s.DB).Where("username = ?", user.Username).Update(ctx, "is_admin", isAdmin); err != nil {
				slog.Error("Failed to update admin status", "error", err)
			} else {
				user.IsAdmin = isAdmin
				slog.Info("Updated admin status via OIDC groups", "username", user.Username, "isAdmin", isAdmin)
			}
		}
	}
}

// extractGroupsFromClaims handles dynamic group claim extraction.
func (s *Server) extractGroupsFromClaims(claims map[string]any, key string) []string {
	var result []string
	val, ok := claims[key]
	if !ok {
		return result
	}

	switch v := val.(type) {
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
	case []string:
		return v
	case string:
		result = append(result, v)
	}
	return result
}

// generateSecureTokenEncoded generates a URL-safe secure token.
func generateSecureTokenEncoded(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
