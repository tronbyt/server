package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"tronbyt-server/internal/connections"
	"tronbyt-server/internal/data"

	"golang.org/x/oauth2"
)

// connectionPendingKey is the session key under which we stash the
// in-flight OAuth state (provider, state token, return URL). One entry at a
// time per session — clicking "Connect" twice abandons the older flow.
const connectionPendingKey = "conn_pending"

// connectionPending is what we marshal into session for the round-trip.
type connectionPending struct {
	State    string `json:"state"`
	Provider string `json:"provider"`
	ReturnTo string `json:"return_to"`
	Scopes   string `json:"scopes"` // space-separated
}

// callbackPath is the single stable redirect URI used for all providers.
// Admins must register this exact path with each OAuth provider, e.g.
// https://my-tronbyt.example.com/oauth-callback.
const callbackPath = "/oauth-callback"

// connectionCallbackURL builds the absolute redirect URI for the current
// request, honouring X-Forwarded-* via ProxyMiddleware.
func (s *Server) connectionCallbackURL(r *http.Request) string {
	return strings.TrimRight(s.GetBaseURL(r), "/") + callbackPath
}

// ConnectionProviderView is one row on the /connections page: a provider
// the admin has configured, plus this user's connection state for it.
type ConnectionProviderView struct {
	Name         string
	DisplayName  string
	Connected    bool
	Label        string
	Scopes       string
	ConnectionID uint
	// DeviceFlow marks providers connected by short code rather than a
	// browser redirect — the Connect link points at the device flow and
	// the code is shown on the display.
	DeviceFlow bool
}

// anyConnectionProviderConfigured reports whether the admin supplied
// credentials for at least one provider. Drives the nav link.
func (s *Server) anyConnectionProviderConfigured() bool {
	if s.Connections == nil || s.ConnectionsRegistry == nil {
		return false
	}
	for _, p := range s.ConnectionsRegistry.All() {
		if s.Connections.CodeFlowAvailable(p) || s.Connections.DeviceFlowAvailable(p) {
			return true
		}
	}
	return false
}

// handleConnectionsPage lists the configured providers and the user's
// connection state for each, with Connect/Disconnect controls.
//
//	GET /connections
func (s *Server) handleConnectionsPage(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)

	conns, err := s.Connections.ListForUser(r.Context(), user.Username)
	if err != nil {
		slog.Error("Connections page: list failed", "user", user.Username, "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	byProvider := make(map[string]data.Connection, len(conns))
	for _, c := range conns {
		byProvider[c.Provider] = c
	}

	var views []ConnectionProviderView
	for _, p := range s.ConnectionsRegistry.All() {
		codeFlow := s.Connections.CodeFlowAvailable(p)
		deviceFlow := s.Connections.DeviceFlowAvailable(p)
		if !codeFlow && !deviceFlow {
			continue // provider not enabled on this server
		}
		// Prefer the device flow where it's available: no redirect URI to
		// register, and the code can be read straight off the display.
		view := ConnectionProviderView{Name: p.Name, DisplayName: p.DisplayName, DeviceFlow: deviceFlow}
		if c, ok := byProvider[p.Name]; ok {
			view.Connected = true
			view.Label = c.DisplayName
			if view.Label == "" {
				view.Label = c.ExternalID
			}
			view.Scopes = c.Scopes
			view.ConnectionID = c.ID
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].DisplayName < views[j].DisplayName })

	s.renderTemplate(w, r, "connections", TemplateData{
		ConnectionProviders: views,
	})
}

// handleConnectionStart kicks off the OAuth dance for one provider. It is
// called by the "Connect Strava" button on the app config page or the
// /connections management page.
//
//	GET /connections/start/{provider}?return_to=...&scopes=read,activity:read
func (s *Server) handleConnectionStart(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	providerName := strings.ToLower(r.PathValue("provider"))

	provider, ok := s.ConnectionsRegistry.Get(providerName)
	if !ok {
		http.Error(w, "Unknown provider", http.StatusNotFound)
		return
	}

	clientID, clientSecret := s.Connections.GetCreds(provider.Name)
	if clientID == "" || clientSecret == "" {
		slog.Warn("Connection start: provider not configured", "provider", provider.Name, "user", user.Username)
		http.Error(w, "This provider is not configured on the server. Ask your admin to set the "+strings.ToUpper(provider.Name)+"_CLIENT_ID/_SECRET env vars.", http.StatusServiceUnavailable)
		return
	}

	state, err := generateSecureTokenEncoded(32)
	if err != nil {
		slog.Error("Failed to generate connection state", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	returnTo := r.URL.Query().Get("return_to")
	if !isSafeReturnTo(returnTo) {
		returnTo = "/"
	}

	// Scopes can be passed as comma- or space-separated; normalize to space.
	scopes := normalizeScopes(r.URL.Query().Get("scopes"))
	if scopes == "" {
		// Record what we are actually about to request. Providers that
		// don't echo `scope` back in the token response would otherwise
		// leave the stored set empty, and nothing downstream could tell
		// what the connection is good for.
		scopes = strings.Join(provider.DefaultScopes, " ")
	}

	pending := connectionPending{
		State:    state,
		Provider: provider.Name,
		ReturnTo: returnTo,
		Scopes:   scopes,
	}

	session, err := s.Store.Get(r, "session-name")
	if err != nil {
		slog.Error("Connection start: get session", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	session.Values[connectionPendingKey] = mustEncodePending(pending)
	if err := s.saveSession(w, r, session); err != nil {
		slog.Error("Connection start: save session", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	redirectURL := s.connectionCallbackURL(r)
	cfg := provider.OAuth2Config(clientID, clientSecret, redirectURL, splitScopes(scopes))

	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// handleConnectionCallback receives the provider's redirect with code+state,
// verifies state against session, exchanges the code, and persists the
// Connection. Single endpoint shared across all providers.
//
//	GET /oauth-callback?code=...&state=...
func (s *Server) handleConnectionCallback(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	q := r.URL.Query()

	if errMsg := q.Get("error"); errMsg != "" {
		slog.Warn("Connection callback: provider returned error", "error", errMsg, "desc", q.Get("error_description"), "user", user.Username)
		s.flashAndRedirect(w, r, "Could not connect: "+errMsg, "/", http.StatusSeeOther)
		return
	}

	session, err := s.Store.Get(r, "session-name")
	if err != nil {
		slog.Error("Connection callback: get session", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	rawPending, _ := session.Values[connectionPendingKey].(string)
	delete(session.Values, connectionPendingKey)

	pending, err := decodePending(rawPending)
	if err != nil {
		slog.Warn("Connection callback: missing/invalid pending state", "error", err)
		s.flashAndRedirect(w, r, "Connection request expired or invalid. Try again.", "/", http.StatusSeeOther)
		return
	}

	if got := q.Get("state"); got == "" || got != pending.State {
		slog.Warn("Connection callback: state mismatch", "user", user.Username, "provider", pending.Provider)
		s.flashAndRedirect(w, r, "Connection failed: state mismatch.", "/", http.StatusSeeOther)
		return
	}

	if err := s.saveSession(w, r, session); err != nil {
		slog.Error("Connection callback: save session", "error", err)
	}

	code := q.Get("code")
	if code == "" {
		s.flashAndRedirect(w, r, "Connection failed: no code returned.", pending.ReturnTo, http.StatusSeeOther)
		return
	}

	conn, err := s.Connections.ExchangeCode(
		r.Context(),
		pending.Provider,
		user.Username,
		s.connectionCallbackURL(r),
		code,
		splitScopes(pending.Scopes),
	)
	if err != nil {
		slog.Error("Connection callback: exchange failed", "provider", pending.Provider, "user", user.Username, "error", err)
		switch {
		case errors.Is(err, connections.ErrProviderDisabled):
			s.flashAndRedirect(w, r, "Provider not configured on the server.", pending.ReturnTo, http.StatusSeeOther)
		default:
			s.flashAndRedirect(w, r, "Could not finish connection.", pending.ReturnTo, http.StatusSeeOther)
		}
		return
	}

	slog.Info("Connection established", "provider", conn.Provider, "user", user.Username, "external_id", conn.ExternalID)
	http.Redirect(w, r, pending.ReturnTo, http.StatusSeeOther)
}

// handleConnectionDisconnect deletes one of the user's connections.
//
//	POST /connections/{id}/disconnect
func (s *Server) handleConnectionDisconnect(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	idStr := r.PathValue("id")
	id, err := strconv.ParseUint(idStr, 10, 32)
	if err != nil {
		http.Error(w, "Bad id", http.StatusBadRequest)
		return
	}

	if err := s.Connections.Disconnect(r.Context(), uint(id), user.Username); err != nil && !errors.Is(err, connections.ErrConnectionNotFound) {
		slog.Error("Connection disconnect failed", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	returnTo := r.FormValue("return_to")
	if !isSafeReturnTo(returnTo) {
		returnTo = "/connections"
	}
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}

// --- helpers ---

// isSafeReturnTo accepts only same-origin paths to prevent open-redirect
// abuse. Anything starting with "//" or containing a scheme is rejected.
// Backslashes are rejected outright: url.Parse treats "\" as an ordinary
// path byte, but browsers normalize "/\evil.com" to the protocol-relative
// "//evil.com", so a backslash anywhere would defeat the "//" guard.
func isSafeReturnTo(p string) bool {
	if p == "" {
		return false
	}
	if !strings.HasPrefix(p, "/") {
		return false
	}
	if strings.HasPrefix(p, "//") {
		return false
	}
	if strings.Contains(p, "\\") {
		return false
	}
	if u, err := url.Parse(p); err != nil || u.Scheme != "" || u.Host != "" {
		return false
	}
	return true
}

func normalizeScopes(s string) string {
	if s == "" {
		return ""
	}
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
	return strings.Join(parts, " ")
}

func splitScopes(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Fields(s)
}

func mustEncodePending(p connectionPending) string {
	b, err := json.Marshal(p)
	if err != nil {
		// connectionPending is plain strings; marshal cannot fail.
		panic(err)
	}
	return string(b)
}

func decodePending(raw string) (connectionPending, error) {
	var p connectionPending
	if raw == "" {
		return p, errors.New("no pending state in session")
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return p, err
	}
	if p.State == "" || p.Provider == "" {
		return p, errors.New("incomplete pending state")
	}
	return p, nil
}
