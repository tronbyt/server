package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"tronbyt-server/internal/data"
	"tronbyt-server/internal/renderer"

	securejoin "github.com/cyphar/filepath-securejoin"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

// oauthRefreshMargin is how close to expiry an access token gets refreshed
// before it is handed to an app.
const oauthRefreshMargin = time.Minute

// oauthHTTPTimeout bounds every request to a provider's token endpoint.
const oauthHTTPTimeout = 30 * time.Second

// oauthReservedAuthParams are authorization request parameters the server sets
// itself; an app's authorization_params cannot override them.
var oauthReservedAuthParams = map[string]bool{
	"client_id":             true,
	"redirect_uri":          true,
	"response_type":         true,
	"scope":                 true,
	"state":                 true,
	"code_challenge":        true,
	"code_challenge_method": true,
}

// oauthSchemaField is the part of an app's schema field that server-managed
// OAuth reads. A field is server-managed when it declares a token endpoint;
// fields without one use Tidbyt's original, app-handled flow.
type oauthSchemaField struct {
	Type                  string            `json:"type"`
	ID                    string            `json:"id"`
	Name                  string            `json:"name"`
	AuthorizationEndpoint string            `json:"authorization_endpoint"`
	TokenEndpoint         string            `json:"token_endpoint"`
	Scopes                []string          `json:"scopes"`
	AuthorizationParams   map[string]string `json:"authorization_params"`
}

func (f oauthSchemaField) serverManaged() bool {
	return f.TokenEndpoint != ""
}

// oauthSchemaFields returns the OAuth2 fields of an app schema.
func oauthSchemaFields(schemaJSON []byte) []oauthSchemaField {
	var schema struct {
		Fields []oauthSchemaField `json:"schema"`
	}
	if len(schemaJSON) == 0 || json.Unmarshal(schemaJSON, &schema) != nil {
		return nil
	}
	var fields []oauthSchemaField
	for _, f := range schema.Fields {
		if f.Type == "oauth2" {
			fields = append(fields, f)
		}
	}
	return fields
}

// serverOAuthField returns the server-managed OAuth2 field with the given ID.
func serverOAuthField(schemaJSON []byte, fieldID string) (oauthSchemaField, bool) {
	for _, f := range oauthSchemaFields(schemaJSON) {
		if f.ID == fieldID && f.serverManaged() {
			return f, true
		}
	}
	return oauthSchemaField{}, false
}

// loadAppSchema returns the app's schema JSON, or nil for apps without one.
func (s *Server) loadAppSchema(ctx context.Context, device *data.Device, app *data.App) ([]byte, error) {
	if app.Path == nil || *app.Path == "" {
		return nil, nil
	}
	appPath, err := securejoin.SecureJoin(s.DataDir, *app.Path)
	if err != nil {
		return nil, fmt.Errorf("resolving app path: %w", err)
	}
	if strings.HasSuffix(strings.ToLower(appPath), ".webp") {
		return nil, nil
	}
	load := renderer.GetSchema
	if s.schemaLoader != nil {
		load = s.schemaLoader
	}
	width, height := device.Type.CanvasSize()
	return load(ctx, appPath, width, height, device.Type.Supports2x())
}

// canUseOAuthClient reports whether the user may sign in to apps with the client.
func canUseOAuthClient(user *data.User, client *data.OAuthClient) bool {
	return client.Shared || client.OwnerUsername == user.Username
}

// canManageOAuthClient reports whether the user may edit or delete the client.
// Shared clients belong to the server, so any admin may manage them.
func canManageOAuthClient(user *data.User, client *data.OAuthClient) bool {
	return client.OwnerUsername == user.Username || (client.Shared && user.IsAdmin)
}

// usableOAuthClients returns the clients the user may use for the issuer.
func (s *Server) usableOAuthClients(ctx context.Context, user *data.User, issuerKey string) ([]data.OAuthClient, error) {
	return gorm.G[data.OAuthClient](s.DB).
		Where("issuer_key = ? AND (owner_username = ? OR shared = ?)", issuerKey, user.Username, true).
		Order("name ASC").
		Find(ctx)
}

// usableOAuthClient loads a client by ID if the user may use it.
func (s *Server) usableOAuthClient(ctx context.Context, user *data.User, id string) (*data.OAuthClient, error) {
	client, err := gorm.G[data.OAuthClient](s.DB).Where("id = ?", id).First(ctx)
	if err != nil {
		return nil, err
	}
	if !canUseOAuthClient(user, &client) {
		return nil, gorm.ErrRecordNotFound
	}
	return &client, nil
}

// oauthRedirectURI is the callback the client's owner registers with the provider.
func (s *Server) oauthRedirectURI(r *http.Request, clientID string) string {
	return s.GetBaseURL(r) + "/oauth/callback/" + url.PathEscape(clientID)
}

func oauth2Config(client *data.OAuthClient, redirectURI string, scopes []string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     client.ClientID,
		ClientSecret: string(client.ClientSecret),
		Endpoint: oauth2.Endpoint{
			AuthURL:   client.AuthorizationEndpoint,
			TokenURL:  client.TokenEndpoint,
			AuthStyle: oauth2.AuthStyleAutoDetect,
		},
		RedirectURL: redirectURI,
		Scopes:      scopes,
	}
}

// oauthContext gives token requests a bounded HTTP client.
func oauthContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: oauthHTTPTimeout})
}

// deleteOAuthClients deletes the given clients and every token issued with them.
func deleteOAuthClients(ctx context.Context, tx *gorm.DB, clientIDs []string) error {
	if len(clientIDs) == 0 {
		return nil
	}
	if _, err := gorm.G[data.OAuthToken](tx).Where("oauth_client_id IN ?", clientIDs).Delete(ctx); err != nil {
		return fmt.Errorf("deleting OAuth tokens: %w", err)
	}
	if _, err := gorm.G[data.OAuthClient](tx).Where("id IN ?", clientIDs).Delete(ctx); err != nil {
		return fmt.Errorf("deleting OAuth clients: %w", err)
	}
	return nil
}

// deleteOAuthTokensForDevices deletes the tokens of every app on the devices.
// Call it in the same transaction that deletes the apps.
func deleteOAuthTokensForDevices(ctx context.Context, tx *gorm.DB, deviceIDs []string) error {
	if len(deviceIDs) == 0 {
		return nil
	}
	if _, err := gorm.G[data.OAuthToken](tx).Where("device_id IN ?", deviceIDs).Delete(ctx); err != nil {
		return fmt.Errorf("deleting OAuth tokens: %w", err)
	}
	return nil
}

// deleteOAuthTokensForApp deletes the tokens of one app installation.
func deleteOAuthTokensForApp(ctx context.Context, tx *gorm.DB, app *data.App) error {
	if _, err := gorm.G[data.OAuthToken](tx).Where("app_id = ?", app.ID).Delete(ctx); err != nil {
		return fmt.Errorf("deleting OAuth tokens: %w", err)
	}
	return nil
}

// appOAuthTokens returns the stored tokens of an app installation.
func (s *Server) appOAuthTokens(ctx context.Context, app *data.App) ([]data.OAuthToken, error) {
	return gorm.G[data.OAuthToken](s.DB).
		Where("app_id = ? AND device_id = ? AND iname = ?", app.ID, app.DeviceID, app.Iname).
		Find(ctx)
}

// injectOAuthTokens sets each OAuth2 field of the app's config to a fresh
// access token, or to "" when the app is not connected or needs a new sign-in.
// The tokens never reach App.Config in the database.
func (s *Server) injectOAuthTokens(ctx context.Context, app *data.App, config map[string]any) {
	tokens, err := s.appOAuthTokens(ctx, app)
	if err != nil {
		slog.Error("Failed to load OAuth tokens", "app", app.Iname, "error", err)
		return
	}
	for i := range tokens {
		config[tokens[i].FieldID] = s.oauthAccessToken(ctx, &tokens[i])
	}
}

// oauthAccessToken returns a usable access token, refreshing it when it is
// about to expire. It returns "" when the user has to sign in again.
func (s *Server) oauthAccessToken(ctx context.Context, tok *data.OAuthToken) string {
	if tok.Status == data.OAuthTokenReauthRequired {
		return ""
	}
	if !oauthTokenNeedsRefresh(tok, time.Now()) {
		return string(tok.AccessToken)
	}

	// Serialize refreshes of one token: providers that rotate refresh tokens
	// invalidate the old one, so two concurrent refreshes would lose the token.
	lock, _ := s.oauthRefreshLocks.LoadOrStore(tok.ID, &sync.Mutex{})
	mu := lock.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// Another render may have refreshed it while we waited.
	current, err := gorm.G[data.OAuthToken](s.DB).Where("id = ?", tok.ID).First(ctx)
	if err != nil {
		slog.Error("Failed to reload OAuth token", "token_id", tok.ID, "error", err)
		return usableUntilExpiry(tok, time.Now())
	}
	*tok = current
	if tok.Status == data.OAuthTokenReauthRequired {
		return ""
	}
	if !oauthTokenNeedsRefresh(tok, time.Now()) {
		return string(tok.AccessToken)
	}

	if tok.RefreshToken == "" {
		if tok.Expiry != nil && !time.Now().Before(*tok.Expiry) {
			s.markOAuthTokenReauth(ctx, tok, "access token expired and there is no refresh token")
			return ""
		}
		return string(tok.AccessToken)
	}

	client, err := gorm.G[data.OAuthClient](s.DB).Where("id = ?", tok.OAuthClientID).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			s.markOAuthTokenReauth(ctx, tok, "OAuth client no longer exists")
			return ""
		}
		slog.Error("Failed to load OAuth client for refresh", "client_id", tok.OAuthClientID, "error", err)
		return usableUntilExpiry(tok, time.Now())
	}

	// The redirect URI and scopes are not part of a refresh request.
	cfg := oauth2Config(&client, "", nil)
	fresh, err := cfg.TokenSource(oauthContext(ctx), &oauth2.Token{RefreshToken: string(tok.RefreshToken)}).Token()
	if err != nil {
		var retrieveErr *oauth2.RetrieveError
		if errors.As(err, &retrieveErr) && retrieveErr.Response != nil &&
			(retrieveErr.Response.StatusCode == http.StatusBadRequest || retrieveErr.Response.StatusCode == http.StatusUnauthorized) {
			// invalid_grant and friends: the refresh token was revoked or expired.
			s.markOAuthTokenReauth(ctx, tok, err.Error())
			return ""
		}
		slog.Warn("Failed to refresh OAuth token, will retry on next render", "token_id", tok.ID, "error", err)
		return usableUntilExpiry(tok, time.Now())
	}

	updates := oauthTokenFields(fresh)
	if updates.RefreshToken == "" {
		// Most providers do not rotate refresh tokens; keep the one we have.
		updates.RefreshToken = tok.RefreshToken
	}
	updates.Status = data.OAuthTokenOK
	if _, err := gorm.G[data.OAuthToken](s.DB).Where("id = ?", tok.ID).
		Select("AccessToken", "RefreshToken", "TokenType", "Expiry", "Status").
		Updates(ctx, updates); err != nil {
		slog.Error("Failed to save refreshed OAuth token", "token_id", tok.ID, "error", err)
	}
	tok.AccessToken = updates.AccessToken
	tok.RefreshToken = updates.RefreshToken
	tok.TokenType = updates.TokenType
	tok.Expiry = updates.Expiry
	tok.Status = updates.Status
	return string(tok.AccessToken)
}

func (s *Server) markOAuthTokenReauth(ctx context.Context, tok *data.OAuthToken, reason string) {
	slog.Info("OAuth token needs a new sign-in", "token_id", tok.ID, "app", tok.Iname, "reason", reason)
	tok.Status = data.OAuthTokenReauthRequired
	if _, err := gorm.G[data.OAuthToken](s.DB).Where("id = ?", tok.ID).Update(ctx, "status", data.OAuthTokenReauthRequired); err != nil {
		slog.Error("Failed to mark OAuth token for re-authentication", "token_id", tok.ID, "error", err)
	}
}

func oauthTokenNeedsRefresh(tok *data.OAuthToken, now time.Time) bool {
	return tok.AccessToken == "" || (tok.Expiry != nil && tok.Expiry.Sub(now) < oauthRefreshMargin)
}

// usableUntilExpiry returns the stored access token while it is still valid.
func usableUntilExpiry(tok *data.OAuthToken, now time.Time) string {
	if tok.Expiry != nil && !now.Before(*tok.Expiry) {
		return ""
	}
	return string(tok.AccessToken)
}

// oauthTokenFields copies a provider's token response into a model.
func oauthTokenFields(t *oauth2.Token) data.OAuthToken {
	tok := data.OAuthToken{
		AccessToken:  data.Secret(t.AccessToken),
		RefreshToken: data.Secret(t.RefreshToken),
		TokenType:    t.Type(),
	}
	if !t.Expiry.IsZero() {
		expiry := t.Expiry
		tok.Expiry = &expiry
	}
	return tok
}
