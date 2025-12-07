package server

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"log/slog"
	"tronbyt-server/internal/data"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Wrapper for data.User to satisfy webauthn.User interface
type WebAuthnUser struct {
	User        *data.User
	Credentials []webauthn.Credential
}

func (u WebAuthnUser) WebAuthnID() []byte {
	return []byte(u.User.Username)
}

func (u WebAuthnUser) WebAuthnName() string {
	return u.User.Username
}

func (u WebAuthnUser) WebAuthnDisplayName() string {
	return u.User.Username
}

func (u WebAuthnUser) WebAuthnIcon() string {
	return ""
}

func (u WebAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.Credentials
}

func (s *Server) initWebAuthn(r *http.Request) (*webauthn.WebAuthn, error) {
	scheme := "http"
	if r.TLS != nil || r.URL.Scheme == "https" {
		scheme = "https"
	}
	origin := fmt.Sprintf("%s://%s", scheme, r.Host)

	return webauthn.New(&webauthn.Config{
		RPDisplayName: "Tronbyt Server",
		RPID:          strings.Split(r.Host, ":")[0], // Hostname without port
		RPOrigins:     []string{origin},
	})
}

func parseTransports(t string) []protocol.AuthenticatorTransport {
	if t == "" {
		return nil
	}
	parts := strings.Split(t, ",")
	res := make([]protocol.AuthenticatorTransport, len(parts))
	for i, p := range parts {
		res[i] = protocol.AuthenticatorTransport(p)
	}
	return res
}

func joinTransports(t []protocol.AuthenticatorTransport) string {
	parts := make([]string, len(t))
	for i, p := range t {
		parts[i] = string(p)
	}
	return strings.Join(parts, ",")
}

// Handlers

func (s *Server) handleWebAuthnRegisterBegin(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var user data.User
	if err := s.DB.Preload("Credentials").First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	var credentials []webauthn.Credential
	for _, cred := range user.Credentials {
		idBytes, err := base64.URLEncoding.DecodeString(cred.ID)
		if err != nil {
			slog.Warn("Failed to decode credential ID", "id", cred.ID, "error", err)
			continue
		}
		aaguid, _ := hex.DecodeString(cred.Authenticator) // Ignore error, default nil
		credentials = append(credentials, webauthn.Credential{
			ID:              idBytes,
			PublicKey:       cred.PublicKey,
			AttestationType: cred.AttestationType,
			Transport:       parseTransports(cred.Transport),
			Authenticator:   webauthn.Authenticator{AAGUID: aaguid, SignCount: cred.SignCount, CloneWarning: cred.CloneWarning},
		})
	}

	webAuthnUser := WebAuthnUser{User: &user, Credentials: credentials}

	wa, err := s.initWebAuthn(r)
	if err != nil {
		http.Error(w, "WebAuthn init failed", http.StatusInternalServerError)
		return
	}

	// Exclude existing credentials
	registerOptions := func(credCreationOpts *protocol.PublicKeyCredentialCreationOptions) {
		excludeList := make([]protocol.CredentialDescriptor, len(webAuthnUser.Credentials))
		for i, c := range webAuthnUser.Credentials {
			excludeList[i] = protocol.CredentialDescriptor{
				Type:         protocol.PublicKeyCredentialType,
				CredentialID: c.ID,
				Transport:    c.Transport,
			}
		}
		credCreationOpts.CredentialExcludeList = excludeList
	}

	options, sessionData, err := wa.BeginRegistration(webAuthnUser, registerOptions)
	if err != nil {
		slog.Error("Failed to begin registration", "error", err)
		http.Error(w, "WebAuthn registration failed", http.StatusInternalServerError)
		return
	}

	session.Values["webauthn_session"] = sessionData
	if err := session.Save(r, w); err != nil {
		slog.Error("Failed to save WebAuthn session data", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := json.NewEncoder(w).Encode(options); err != nil {
		slog.Error("Failed to encode WebAuthn registration options", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

func (s *Server) handleWebAuthnRegisterFinish(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	username, ok := session.Values["username"].(string)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	sessionDataVal := session.Values["webauthn_session"]
	if sessionDataVal == nil {
		http.Error(w, "No registration session found", http.StatusBadRequest)
		return
	}

	sessionData, ok := sessionDataVal.(webauthn.SessionData)
	if !ok {
		http.Error(w, "Invalid session data", http.StatusInternalServerError)
		return
	}

	var user data.User
	if err := s.DB.First(&user, "username = ?", username).Error; err != nil {
		http.Error(w, "User not found", http.StatusInternalServerError)
		return
	}

	webAuthnUser := WebAuthnUser{User: &user}

	wa, err := s.initWebAuthn(r)
	if err != nil {
		http.Error(w, "WebAuthn init failed", http.StatusInternalServerError)
		return
	}

	credential, err := wa.FinishRegistration(webAuthnUser, sessionData, r)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to finish registration: %v", err), http.StatusInternalServerError)
		return
	}

	newCred := data.WebAuthnCredential{
		ID:              base64.URLEncoding.EncodeToString(credential.ID),
		UserID:          user.Username,
		PublicKey:       credential.PublicKey,
		AttestationType: credential.AttestationType,
		Transport:       joinTransports(credential.Transport),
		Authenticator:   hex.EncodeToString(credential.Authenticator.AAGUID),
		SignCount:       credential.Authenticator.SignCount,
		CloneWarning:    credential.Authenticator.CloneWarning,
	}

	if err := s.DB.Create(&newCred).Error; err != nil {
		http.Error(w, "Failed to save credential", http.StatusInternalServerError)
		return
	}

	delete(session.Values, "webauthn_session")
	if err := session.Save(r, w); err != nil {
		slog.Error("Failed to save session after WebAuthn registration", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleWebAuthnLoginBegin(w http.ResponseWriter, r *http.Request) {
	wa, err := s.initWebAuthn(r)
	if err != nil {
		http.Error(w, "WebAuthn init failed", http.StatusInternalServerError)
		return
	}

	// Discoverable login
	options, sessionData, err := wa.BeginDiscoverableLogin()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to begin login: %v", err), http.StatusInternalServerError)
		return
	}

	session, _ := s.Store.Get(r, "session-name")
	session.Values["webauthn_session"] = sessionData
	if err := session.Save(r, w); err != nil {
		slog.Error("Failed to save WebAuthn login session data", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if err := json.NewEncoder(w).Encode(options); err != nil {
		slog.Error("Failed to encode WebAuthn login options", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

func (s *Server) handleWebAuthnLoginFinish(w http.ResponseWriter, r *http.Request) {
	session, _ := s.Store.Get(r, "session-name")
	sessionDataVal := session.Values["webauthn_session"]
	if sessionDataVal == nil {
		http.Error(w, "No login session found", http.StatusBadRequest)
		return
	}

	sessionData, ok := sessionDataVal.(webauthn.SessionData)
	if !ok {
		http.Error(w, "Invalid session data", http.StatusInternalServerError)
		return
	}

	wa, err := s.initWebAuthn(r)
	if err != nil {
		http.Error(w, "WebAuthn init failed", http.StatusInternalServerError)
		return
	}

	credential, err := wa.FinishLogin(nil, sessionData, r)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to finish login: %v", err), http.StatusInternalServerError)
		return
	}

	var cred data.WebAuthnCredential
	if err := s.DB.First(&cred, "id = ?", base64.URLEncoding.EncodeToString(credential.ID)).Error; err != nil {
		http.Error(w, "Credential not found", http.StatusUnauthorized)
		return
	}

	cred.SignCount = credential.Authenticator.SignCount
	cred.CloneWarning = credential.Authenticator.CloneWarning
	if err := s.DB.Save(&cred).Error; err != nil {
		slog.Error("Failed to update credential", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	session.Values["username"] = cred.UserID
	delete(session.Values, "webauthn_session")
	if err := session.Save(r, w); err != nil {
		slog.Error("Failed to save session after WebAuthn login", "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
}
