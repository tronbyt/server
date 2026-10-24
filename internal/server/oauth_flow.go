package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/oauth"

	"golang.org/x/oauth2"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// oauthPendingSessionKey holds the sign-in in progress, between the redirect
// to the provider and its callback. Only one sign-in can be in flight.
const oauthPendingSessionKey = "oauth_pending"

// oauthPendingTTL is how long the user has to finish signing in at the provider.
const oauthPendingTTL = 10 * time.Minute

// oauthPending is stored in the signed session cookie as JSON.
type oauthPending struct {
	State       string   `json:"state"`
	Verifier    string   `json:"verifier"`
	ClientID    string   `json:"client_id"` // OAuthClient.ID
	DeviceID    string   `json:"device_id"`
	Iname       string   `json:"iname"`
	FieldID     string   `json:"field_id"`
	RedirectURI string   `json:"redirect_uri"`
	Scopes      []string `json:"scopes"`
	CreatedAt   int64    `json:"created_at"`
}

func appConfigPath(deviceID, iname string) string {
	return "/devices/" + url.PathEscape(deviceID) + "/" + url.PathEscape(iname) + "/config"
}

func appOAuthFieldPath(deviceID, iname, fieldID, action string) string {
	return "/devices/" + url.PathEscape(deviceID) + "/" + url.PathEscape(iname) + "/oauth/" + url.PathEscape(fieldID) + "/" + action
}

// handleOAuthConnect starts signing in to a provider for an app's OAuth2 field
// with an OAuth client the user picked.
func (s *Server) handleOAuthConnect(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)
	app := GetApp(r)
	fieldID := r.PathValue("field")
	configPath := appConfigPath(device.ID, app.Iname)

	schemaBytes, err := s.loadAppSchema(r.Context(), device, app)
	if err != nil {
		slog.Error("Failed to get app schema for OAuth sign-in", "app", app.Iname, "error", err)
		s.flashAndRedirect(w, r, "Could not load the app's settings.", configPath, http.StatusSeeOther)
		return
	}
	field, ok := serverOAuthField(schemaBytes, fieldID)
	if !ok {
		s.flashAndRedirect(w, r, "This app field does not support sign-in on this server.", configPath, http.StatusSeeOther)
		return
	}
	issuerKey, err := oauth.IssuerKey(field.AuthorizationEndpoint, field.TokenEndpoint)
	if err != nil {
		slog.Warn("App declares invalid OAuth endpoints", "app", app.Iname, "field", fieldID, "error", err)
		s.flashAndRedirect(w, r, "This app declares invalid OAuth endpoints.", configPath, http.StatusSeeOther)
		return
	}
	client, err := s.usableOAuthClient(r.Context(), user, r.FormValue("client"))
	if err != nil || client.IssuerKey != issuerKey {
		s.flashAndRedirect(w, r, "The selected OAuth client cannot be used with this app.", configPath, http.StatusSeeOther)
		return
	}

	state, err := generateSecureToken(32)
	if err != nil {
		slog.Error("Failed to generate OAuth state", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	pending := oauthPending{
		State:       state,
		Verifier:    oauth2.GenerateVerifier(),
		ClientID:    client.ID,
		DeviceID:    device.ID,
		Iname:       app.Iname,
		FieldID:     fieldID,
		RedirectURI: s.oauthRedirectURI(r, client.ID),
		Scopes:      field.Scopes,
		CreatedAt:   time.Now().Unix(),
	}
	pendingJSON, err := json.Marshal(pending)
	if err != nil {
		slog.Error("Failed to encode OAuth sign-in state", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	session, _ := s.Store.Get(r, "session-name")
	session.Values[oauthPendingSessionKey] = string(pendingJSON)
	if err := s.saveSession(w, r, session); err != nil {
		slog.Error("Failed to save session", "error", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	opts := []oauth2.AuthCodeOption{oauth2.S256ChallengeOption(pending.Verifier)}
	for k, v := range field.AuthorizationParams {
		if !oauthReservedAuthParams[k] {
			opts = append(opts, oauth2.SetAuthURLParam(k, v))
		}
	}
	authURL := oauth2Config(client, pending.RedirectURI, pending.Scopes).AuthCodeURL(state, opts...)
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// handleOAuthCallback finishes a sign-in: it checks the state, exchanges the
// code for tokens with the client's secret and stores them for the app.
func (s *Server) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	session, _ := s.Store.Get(r, "session-name")
	raw, _ := session.Values[oauthPendingSessionKey].(string)
	delete(session.Values, oauthPendingSessionKey)
	if err := s.saveSession(w, r, session); err != nil {
		slog.Error("Failed to save session", "error", err)
	}

	var pending oauthPending
	query := r.URL.Query()
	if raw == "" || json.Unmarshal([]byte(raw), &pending) != nil ||
		subtle.ConstantTimeCompare([]byte(pending.State), []byte(query.Get("state"))) != 1 ||
		pending.ClientID != r.PathValue("client") {
		s.flashAndRedirect(w, r, "The sign-in link is invalid or was already used. Please try again.", "/", http.StatusSeeOther)
		return
	}
	configPath := appConfigPath(pending.DeviceID, pending.Iname)
	if time.Since(time.Unix(pending.CreatedAt, 0)) > oauthPendingTTL {
		s.flashAndRedirect(w, r, "The sign-in took too long. Please try again.", configPath, http.StatusSeeOther)
		return
	}
	if providerErr := query.Get("error"); providerErr != "" {
		detail := providerErr
		if desc := query.Get("error_description"); desc != "" {
			detail += ": " + desc
		}
		slog.Info("OAuth provider returned an error", "error", detail)
		s.flashTextAndRedirect(w, r, s.localizeOrID(s.getLocalizer(r), "Sign-in failed.")+" "+detail, configPath)
		return
	}

	device, app := findUserApp(user, pending.DeviceID, pending.Iname)
	if app == nil {
		s.flashAndRedirect(w, r, "The app no longer exists.", "/", http.StatusSeeOther)
		return
	}
	client, err := s.usableOAuthClient(r.Context(), user, pending.ClientID)
	if err != nil {
		s.flashAndRedirect(w, r, "The OAuth client no longer exists.", configPath, http.StatusSeeOther)
		return
	}

	ctx, cancel := context.WithTimeout(oauthContext(r.Context()), oauthHTTPTimeout)
	defer cancel()
	token, err := oauth2Config(client, pending.RedirectURI, pending.Scopes).
		Exchange(ctx, query.Get("code"), oauth2.VerifierOption(pending.Verifier))
	if err != nil {
		slog.Warn("OAuth code exchange failed", "client_id", client.ID, "error", err)
		s.flashAndRedirect(w, r, "Sign-in failed: the provider did not accept the authorization code.", configPath, http.StatusSeeOther)
		return
	}

	stored := oauthTokenFields(token)
	stored.AppID = app.ID
	stored.FieldID = pending.FieldID
	stored.DeviceID = device.ID
	stored.Iname = app.Iname
	stored.OAuthClientID = client.ID
	stored.Scopes = strings.Join(pending.Scopes, " ")
	stored.Status = data.OAuthTokenOK
	if err := s.DB.WithContext(r.Context()).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "app_id"}, {Name: "field_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"device_id", "iname", "oauth_client_id", "access_token", "refresh_token",
			"token_type", "expiry", "scopes", "status", "updated_at",
		}),
	}).Create(&stored).Error; err != nil {
		slog.Error("Failed to save OAuth token", "app", app.Iname, "error", err)
		s.flashAndRedirect(w, r, "Could not save the sign-in.", configPath, http.StatusSeeOther)
		return
	}

	// Render with the new token right away.
	app.LastRender = time.Time{}
	s.possiblyRender(r.Context(), app, device, user)
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	http.Redirect(w, r, configPath, http.StatusSeeOther)
}

// handleOAuthDisconnect forgets the token of an app's OAuth2 field.
func (s *Server) handleOAuthDisconnect(w http.ResponseWriter, r *http.Request) {
	user := GetUser(r)
	device := GetDevice(r)
	app := GetApp(r)

	if _, err := gorm.G[data.OAuthToken](s.DB).
		Where("app_id = ? AND field_id = ?", app.ID, r.PathValue("field")).
		Delete(r.Context()); err != nil {
		slog.Error("Failed to delete OAuth token", "app", app.Iname, "error", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	app.LastRender = time.Time{}
	s.possiblyRender(r.Context(), app, device, user)
	s.notifyDashboard(user.Username, WSEvent{Type: "apps_changed", DeviceID: device.ID})

	http.Redirect(w, r, appConfigPath(device.ID, app.Iname), http.StatusSeeOther)
}

// findUserApp looks an app up among the devices preloaded for the user.
func findUserApp(user *data.User, deviceID, iname string) (*data.Device, *data.App) {
	for i := range user.Devices {
		device := &user.Devices[i]
		if device.ID != deviceID {
			continue
		}
		for _, app := range device.Apps {
			if app.Iname == iname {
				return device, app
			}
		}
	}
	return nil, nil
}

func (s *Server) flashTextAndRedirect(w http.ResponseWriter, r *http.Request, text, redirectURL string) {
	session, _ := s.Store.Get(r, "session-name")
	session.AddFlash(text)
	if err := s.saveSession(w, r, session); err != nil {
		slog.Error("Failed to save session", "error", err)
	}
	http.Redirect(w, r, redirectURL, http.StatusSeeOther)
}

// oauthFieldView is what the config page needs to draw one OAuth2 field.
type oauthFieldView struct {
	ServerManaged bool                 `json:"server_managed"`
	Invalid       bool                 `json:"invalid,omitempty"` // the app declares bad endpoints
	Clients       []oauthClientOption  `json:"clients"`
	Connected     *oauthConnectionView `json:"connected,omitempty"`
	ConnectURL    string               `json:"connect_url,omitempty"`
	DisconnectURL string               `json:"disconnect_url,omitempty"`
	NewClientURL  string               `json:"new_client_url,omitempty"`
}

type oauthClientOption struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Shared bool   `json:"shared"`
}

type oauthConnectionView struct {
	ClientName string `json:"client_name"`
	// ReauthRequired is set when the provider rejected the refresh token.
	ReauthRequired bool `json:"reauth_required"`
	// Mismatch is set when the app now declares other endpoints than the
	// client the token came from.
	Mismatch bool `json:"mismatch"`
}

// oauthFieldViews builds the config page's view of the app's OAuth2 fields,
// keyed by field ID, as JSON for the page's script.
func (s *Server) oauthFieldViews(ctx context.Context, user *data.User, device *data.Device, app *data.App, schemaBytes []byte) template.JS {
	fields := oauthSchemaFields(schemaBytes)
	views := make(map[string]oauthFieldView, len(fields))
	if len(fields) > 0 {
		tokens, err := s.appOAuthTokens(ctx, app)
		if err != nil {
			slog.Error("Failed to load OAuth tokens", "app", app.Iname, "error", err)
		}
		for _, field := range fields {
			views[field.ID] = s.oauthFieldView(ctx, user, device, app, field, tokens)
		}
	}
	b, err := json.Marshal(views)
	if err != nil {
		slog.Error("Failed to encode OAuth fields", "error", err)
		return "{}"
	}
	return template.JS(b)
}

func (s *Server) oauthFieldView(ctx context.Context, user *data.User, device *data.Device, app *data.App, field oauthSchemaField, tokens []data.OAuthToken) oauthFieldView {
	view := oauthFieldView{ServerManaged: field.serverManaged(), Clients: []oauthClientOption{}}
	if !view.ServerManaged {
		return view
	}
	issuerKey, err := oauth.IssuerKey(field.AuthorizationEndpoint, field.TokenEndpoint)
	if err != nil {
		view.Invalid = true
		return view
	}

	clients, err := s.usableOAuthClients(ctx, user, issuerKey)
	if err != nil {
		slog.Error("Failed to load OAuth clients", "error", err)
	}
	for _, c := range clients {
		view.Clients = append(view.Clients, oauthClientOption{ID: c.ID, Name: c.Name, Shared: c.Shared})
	}

	for _, tok := range tokens {
		if tok.FieldID != field.ID {
			continue
		}
		conn := &oauthConnectionView{ReauthRequired: tok.Status == data.OAuthTokenReauthRequired}
		client, err := gorm.G[data.OAuthClient](s.DB).Where("id = ?", tok.OAuthClientID).First(ctx)
		switch {
		case err == nil:
			conn.ClientName = client.Name
			conn.Mismatch = client.IssuerKey != issuerKey
		case errors.Is(err, gorm.ErrRecordNotFound):
			conn.Mismatch = true
		default:
			slog.Error("Failed to load OAuth client", "client_id", tok.OAuthClientID, "error", err)
		}
		view.Connected = conn
	}

	view.ConnectURL = appOAuthFieldPath(device.ID, app.Iname, field.ID, "connect")
	view.DisconnectURL = appOAuthFieldPath(device.ID, app.Iname, field.ID, "disconnect")
	newClient := url.Values{
		"name":                   {field.Name},
		"authorization_endpoint": {field.AuthorizationEndpoint},
		"token_endpoint":         {field.TokenEndpoint},
		"return_to":              {appConfigPath(device.ID, app.Iname)},
	}
	view.NewClientURL = "/settings/oauth?" + newClient.Encode()
	return view
}
