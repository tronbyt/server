package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"tronbyt-server/internal/data"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// fakeProvider is an OAuth2 authorization server for tests.
type fakeProvider struct {
	srv *httptest.Server

	mu            sync.Mutex
	clientID      string
	clientSecret  string
	challenge     string // code_challenge the test saw in the authorization URL
	redirectURI   string
	refreshFails  bool
	refreshCount  int
	exchangeCount int
}

func newFakeProvider(t *testing.T) *fakeProvider {
	p := &fakeProvider{clientID: "cid", clientSecret: "csecret"}
	p.srv = httptest.NewServer(http.HandlerFunc(p.handleToken))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *fakeProvider) authURL() string  { return p.srv.URL + "/authorize" }
func (p *fakeProvider) tokenURL() string { return p.srv.URL + "/token" }

func (p *fakeProvider) handleToken(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if r.URL.Path != "/token" || r.ParseForm() != nil {
		http.NotFound(w, r)
		return
	}
	id, secret, ok := r.BasicAuth()
	if !ok {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	if id != p.clientID || secret != p.clientSecret {
		writeTokenError(w, http.StatusUnauthorized, "invalid_client")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if r.PostForm.Get("code") != "good-code" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != p.challenge ||
			r.PostForm.Get("redirect_uri") != p.redirectURI {
			writeTokenError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		p.exchangeCount++
		_, _ = w.Write([]byte(`{"access_token":"at-1","refresh_token":"rt-1","token_type":"Bearer","expires_in":3600}`))
	case "refresh_token":
		p.refreshCount++
		if p.refreshFails || r.PostForm.Get("refresh_token") != "rt-1" {
			writeTokenError(w, http.StatusBadRequest, "invalid_grant")
			return
		}
		_, _ = fmt.Fprintf(w, `{"access_token":"at-refreshed-%d","token_type":"Bearer","expires_in":3600}`, p.refreshCount)
	default:
		writeTokenError(w, http.StatusBadRequest, "unsupported_grant_type")
	}
}

func writeTokenError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + code + `"}`))
}

// oauthTestEnv is a server with one user, device and app whose schema has a
// server-managed OAuth2 field pointing at a fake provider.
type oauthTestEnv struct {
	s        *Server
	provider *fakeProvider
	user     data.User
	device   data.Device
	app      data.App
	cookies  map[string]*http.Cookie
}

func newOAuthTestEnv(t *testing.T) *oauthTestEnv {
	s := newTestServer(t)
	p := newFakeProvider(t)
	env := &oauthTestEnv{s: s, provider: p, cookies: map[string]*http.Cookie{}}

	env.user = data.User{Username: "alice", APIKey: "alice-key"}
	require.NoError(t, s.DB.Create(&env.user).Error)
	env.device = data.Device{ID: "dev1", Username: "alice", APIKey: "dev1-key"}
	require.NoError(t, s.DB.Create(&env.device).Error)
	appPath := "users/alice/apps/github/github.star"
	env.app = data.App{DeviceID: "dev1", Iname: "100", Name: "github", Path: &appPath, Pushed: true}
	require.NoError(t, s.DB.Create(&env.app).Error)

	s.schemaLoader = func(context.Context, string, int, int, bool) ([]byte, error) {
		return env.schema(p.authURL(), p.tokenURL()), nil
	}
	env.login(t, "alice")
	return env
}

func (env *oauthTestEnv) schema(authURL, tokenURL string) []byte {
	b, _ := json.Marshal(map[string]any{
		"version": "1",
		"schema": []map[string]any{
			{
				"type":                   "oauth2",
				"id":                     "auth",
				"name":                   "GitHub",
				"authorization_endpoint": authURL,
				"token_endpoint":         tokenURL,
				"scopes":                 []string{"read:user", "repo"},
				"authorization_params":   map[string]string{"prompt": "consent", "client_id": "hijack", "redirect_uri": "https://evil.example"},
			},
			{
				"type":                   "oauth2",
				"id":                     "legacy",
				"name":                   "Legacy",
				"client_id":              "tidbyt-client",
				"authorization_endpoint": authURL,
				"handler":                "oauth_handler",
				"scopes":                 []string{"read"},
			},
		},
	})
	return b
}

func (env *oauthTestEnv) login(t *testing.T, username string) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	session, _ := env.s.Store.Get(req, "session-name")
	session.Values["username"] = username
	require.NoError(t, env.s.saveSession(rr, req, session))
	env.cookies = map[string]*http.Cookie{}
	env.keepCookies(rr)
}

func (env *oauthTestEnv) keepCookies(rr *httptest.ResponseRecorder) {
	for _, c := range rr.Result().Cookies() {
		env.cookies[c.Name] = c
	}
}

func (env *oauthTestEnv) do(t *testing.T, method, target string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range env.cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	env.s.ServeHTTP(rr, req)
	env.keepCookies(rr)
	return rr
}

func (env *oauthTestEnv) createClient(t *testing.T, owner string, shared bool, authURL, tokenURL string) data.OAuthClient {
	t.Helper()
	id, err := generateSecureToken(oauthClientIDLength)
	require.NoError(t, err)
	client := data.OAuthClient{
		ID:                    id,
		Name:                  "Client " + id[:4],
		OwnerUsername:         owner,
		Shared:                shared,
		AuthorizationEndpoint: authURL,
		TokenEndpoint:         tokenURL,
		IssuerKey:             authURL + " " + tokenURL,
		ClientID:              env.provider.clientID,
		ClientSecret:          data.Secret(env.provider.clientSecret),
	}
	require.NoError(t, env.s.DB.Create(&client).Error)
	return client
}

// signIn runs connect and the provider callback, returning the callback response.
func (env *oauthTestEnv) signIn(t *testing.T, client data.OAuthClient) *httptest.ResponseRecorder {
	t.Helper()
	rr := env.do(t, http.MethodPost, "/devices/dev1/100/oauth/auth/connect", url.Values{"client": {client.ID}})
	require.Equal(t, http.StatusSeeOther, rr.Code, rr.Body.String())
	authURL, err := url.Parse(rr.Header().Get("Location"))
	require.NoError(t, err)
	q := authURL.Query()

	env.provider.mu.Lock()
	env.provider.challenge = q.Get("code_challenge")
	env.provider.redirectURI = q.Get("redirect_uri")
	env.provider.mu.Unlock()

	return env.do(t, http.MethodGet, "/oauth/callback/"+client.ID+"?code=good-code&state="+url.QueryEscape(q.Get("state")), nil)
}

func (env *oauthTestEnv) tokens(t *testing.T) []data.OAuthToken {
	t.Helper()
	tokens, err := gorm.G[data.OAuthToken](env.s.DB).Find(context.Background())
	require.NoError(t, err)
	return tokens
}

func TestOAuthConnectRedirectsToProvider(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())

	rr := env.do(t, http.MethodPost, "/devices/dev1/100/oauth/auth/connect", url.Values{"client": {client.ID}})
	require.Equal(t, http.StatusSeeOther, rr.Code, rr.Body.String())

	loc, err := url.Parse(rr.Header().Get("Location"))
	require.NoError(t, err)
	assert.Equal(t, env.provider.authURL(), loc.Scheme+"://"+loc.Host+loc.Path)
	q := loc.Query()
	assert.Equal(t, "cid", q.Get("client_id"))
	assert.Equal(t, "code", q.Get("response_type"))
	assert.Equal(t, "read:user repo", q.Get("scope"))
	assert.Equal(t, "http://example.com/oauth/callback/"+client.ID, q.Get("redirect_uri"))
	assert.Equal(t, "S256", q.Get("code_challenge_method"))
	assert.NotEmpty(t, q.Get("code_challenge"))
	assert.Len(t, q.Get("state"), 32)
	assert.Equal(t, "consent", q.Get("prompt"), "app's extra authorization params are passed on")
	assert.NotContains(t, rr.Header().Get("Location"), "hijack", "reserved params cannot be overridden")
	assert.NotContains(t, rr.Header().Get("Location"), "evil.example")
	assert.NotContains(t, rr.Header().Get("Location"), "csecret")
}

func TestOAuthSignInStoresToken(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())

	rr := env.signIn(t, client)
	require.Equal(t, http.StatusSeeOther, rr.Code, rr.Body.String())
	assert.Equal(t, "/devices/dev1/100/config", rr.Header().Get("Location"))

	tokens := env.tokens(t)
	require.Len(t, tokens, 1)
	tok := tokens[0]
	assert.Equal(t, env.app.ID, tok.AppID)
	assert.Equal(t, "auth", tok.FieldID)
	assert.Equal(t, "dev1", tok.DeviceID)
	assert.Equal(t, "100", tok.Iname)
	assert.Equal(t, client.ID, tok.OAuthClientID)
	assert.Equal(t, data.Secret("at-1"), tok.AccessToken)
	assert.Equal(t, data.Secret("rt-1"), tok.RefreshToken)
	assert.Equal(t, "read:user repo", tok.Scopes)
	assert.Equal(t, data.OAuthTokenOK, tok.Status)
	require.NotNil(t, tok.Expiry)
	assert.WithinDuration(t, time.Now().Add(time.Hour), *tok.Expiry, time.Minute)

	// The token stays out of the app's saved config.
	app, err := gorm.G[data.App](env.s.DB).Where("id = ?", env.app.ID).First(context.Background())
	require.NoError(t, err)
	assert.NotContains(t, app.Config, "auth")

	// Signing in again replaces the token instead of adding one.
	rr = env.signIn(t, client)
	require.Equal(t, http.StatusSeeOther, rr.Code)
	assert.Len(t, env.tokens(t), 1)
}

func TestOAuthCallbackRejectsBadState(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())

	rr := env.do(t, http.MethodPost, "/devices/dev1/100/oauth/auth/connect", url.Values{"client": {client.ID}})
	require.Equal(t, http.StatusSeeOther, rr.Code)

	rr = env.do(t, http.MethodGet, "/oauth/callback/"+client.ID+"?code=good-code&state=forged", nil)
	assert.Equal(t, http.StatusSeeOther, rr.Code)
	assert.Equal(t, "/", rr.Header().Get("Location"))
	assert.Empty(t, env.tokens(t))
	assert.Zero(t, env.provider.exchangeCount, "no code exchange without a valid state")
}

func TestOAuthCallbackStateIsSingleUse(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())

	rr := env.do(t, http.MethodPost, "/devices/dev1/100/oauth/auth/connect", url.Values{"client": {client.ID}})
	loc, _ := url.Parse(rr.Header().Get("Location"))
	q := loc.Query()
	env.provider.challenge = q.Get("code_challenge")
	env.provider.redirectURI = q.Get("redirect_uri")
	callback := "/oauth/callback/" + client.ID + "?code=good-code&state=" + url.QueryEscape(q.Get("state"))

	require.Equal(t, "/devices/dev1/100/config", env.do(t, http.MethodGet, callback, nil).Header().Get("Location"))
	require.Equal(t, "/", env.do(t, http.MethodGet, callback, nil).Header().Get("Location"))
	assert.Equal(t, 1, env.provider.exchangeCount)
}

func TestOAuthCallbackProviderError(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())

	rr := env.do(t, http.MethodPost, "/devices/dev1/100/oauth/auth/connect", url.Values{"client": {client.ID}})
	loc, _ := url.Parse(rr.Header().Get("Location"))
	state := loc.Query().Get("state")

	rr = env.do(t, http.MethodGet, "/oauth/callback/"+client.ID+"?error=access_denied&state="+url.QueryEscape(state), nil)
	assert.Equal(t, "/devices/dev1/100/config", rr.Header().Get("Location"))
	assert.Empty(t, env.tokens(t))
}

func TestOAuthConnectRejectsClients(t *testing.T) {
	env := newOAuthTestEnv(t)
	require.NoError(t, env.s.DB.Create(&data.User{Username: "bob", APIKey: "bob-key"}).Error)

	tests := map[string]data.OAuthClient{
		// Same authorization endpoint, but the secret would go elsewhere.
		"other token endpoint":          env.createClient(t, "alice", false, env.provider.authURL(), "https://evil.example/token"),
		"another user's private client": env.createClient(t, "bob", false, env.provider.authURL(), env.provider.tokenURL()),
	}
	for name, client := range tests {
		t.Run(name, func(t *testing.T) {
			rr := env.do(t, http.MethodPost, "/devices/dev1/100/oauth/auth/connect", url.Values{"client": {client.ID}})
			assert.Equal(t, http.StatusSeeOther, rr.Code)
			assert.Equal(t, "/devices/dev1/100/config", rr.Header().Get("Location"))
		})
	}

	t.Run("legacy field", func(t *testing.T) {
		client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())
		rr := env.do(t, http.MethodPost, "/devices/dev1/100/oauth/legacy/connect", url.Values{"client": {client.ID}})
		assert.Equal(t, "/devices/dev1/100/config", rr.Header().Get("Location"))
	})

	t.Run("shared client of another user works", func(t *testing.T) {
		client := env.createClient(t, "bob", true, env.provider.authURL(), env.provider.tokenURL())
		rr := env.do(t, http.MethodPost, "/devices/dev1/100/oauth/auth/connect", url.Values{"client": {client.ID}})
		assert.True(t, strings.HasPrefix(rr.Header().Get("Location"), env.provider.authURL()))
	})
}

func TestOAuthTokensInjectedAndRefreshed(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())
	require.Equal(t, http.StatusSeeOther, env.signIn(t, client).Code)
	ctx := context.Background()

	config := map[string]any{"other": "x"}
	env.s.injectOAuthTokens(ctx, &env.app, config)
	assert.Equal(t, "at-1", config["auth"])
	assert.Equal(t, "x", config["other"])
	assert.Zero(t, env.provider.refreshCount, "a fresh token is used as is")

	// Expire the token: the next render refreshes it once and keeps the refresh token.
	past := time.Now().Add(-time.Minute)
	_, err := gorm.G[data.OAuthToken](env.s.DB).Where("app_id = ?", env.app.ID).Update(ctx, "expiry", past)
	require.NoError(t, err)

	config = map[string]any{}
	env.s.injectOAuthTokens(ctx, &env.app, config)
	assert.Equal(t, "at-refreshed-1", config["auth"])
	tok := env.tokens(t)[0]
	assert.Equal(t, data.Secret("at-refreshed-1"), tok.AccessToken)
	assert.Equal(t, data.Secret("rt-1"), tok.RefreshToken)
	require.NotNil(t, tok.Expiry)
	assert.True(t, tok.Expiry.After(time.Now()))

	config = map[string]any{}
	env.s.injectOAuthTokens(ctx, &env.app, config)
	assert.Equal(t, "at-refreshed-1", config["auth"])
	assert.Equal(t, 1, env.provider.refreshCount)
}

func TestOAuthConcurrentRefreshHappensOnce(t *testing.T) {
	env := newOAuthTestEnv(t)
	// Each connection to a private in-memory SQLite database is a new, empty
	// database, so the goroutines must share one.
	sqlDB, err := env.s.DB.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())
	require.Equal(t, http.StatusSeeOther, env.signIn(t, client).Code)
	ctx := context.Background()
	_, err = gorm.G[data.OAuthToken](env.s.DB).Where("app_id = ?", env.app.ID).Update(ctx, "expiry", time.Now().Add(-time.Minute))
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make([]string, 4)
	for i := range results {
		wg.Go(func() {
			config := map[string]any{}
			env.s.injectOAuthTokens(ctx, &env.app, config)
			results[i], _ = config["auth"].(string)
		})
	}
	wg.Wait()
	for _, r := range results {
		assert.Equal(t, "at-refreshed-1", r)
	}
	assert.Equal(t, 1, env.provider.refreshCount)
}

func TestOAuthRevokedRefreshNeedsReauth(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())
	require.Equal(t, http.StatusSeeOther, env.signIn(t, client).Code)
	ctx := context.Background()
	_, err := gorm.G[data.OAuthToken](env.s.DB).Where("app_id = ?", env.app.ID).Update(ctx, "expiry", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	env.provider.refreshFails = true

	config := map[string]any{}
	env.s.injectOAuthTokens(ctx, &env.app, config)
	assert.Equal(t, "", config["auth"])
	assert.Equal(t, data.OAuthTokenReauthRequired, env.tokens(t)[0].Status)

	// No more refresh attempts until the user signs in again. (The first
	// attempt may count twice: x/oauth2 retries with the other client
	// authentication style when the first one fails.)
	attempts := env.provider.refreshCount
	env.s.injectOAuthTokens(ctx, &env.app, map[string]any{})
	assert.Equal(t, attempts, env.provider.refreshCount)

	views := env.configPageOAuthFields(t)
	require.NotNil(t, views["auth"].Connected)
	assert.True(t, views["auth"].Connected.ReauthRequired)
}

func TestOAuthUnreachableProviderKeepsValidToken(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())
	require.Equal(t, http.StatusSeeOther, env.signIn(t, client).Code)
	ctx := context.Background()
	// About to expire, but not yet expired.
	_, err := gorm.G[data.OAuthToken](env.s.DB).Where("app_id = ?", env.app.ID).Update(ctx, "expiry", time.Now().Add(30*time.Second))
	require.NoError(t, err)
	env.provider.srv.Close()

	config := map[string]any{}
	env.s.injectOAuthTokens(ctx, &env.app, config)
	assert.Equal(t, "at-1", config["auth"])
	assert.Equal(t, data.OAuthTokenOK, env.tokens(t)[0].Status, "a network error is not a revocation")
}

func TestOAuthDisconnect(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())
	require.Equal(t, http.StatusSeeOther, env.signIn(t, client).Code)

	rr := env.do(t, http.MethodPost, "/devices/dev1/100/oauth/auth/disconnect", url.Values{})
	assert.Equal(t, http.StatusSeeOther, rr.Code)
	assert.Empty(t, env.tokens(t))
}

func TestOAuthCrossOriginPostRejected(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())

	req := httptest.NewRequest(http.MethodPost, "/devices/dev1/100/oauth/auth/connect", strings.NewReader("client="+client.ID))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	for _, c := range env.cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	env.s.ServeHTTP(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func (env *oauthTestEnv) configPageOAuthFields(t *testing.T) map[string]oauthFieldView {
	t.Helper()
	user, err := gorm.G[data.User](env.s.DB).Where("username = ?", "alice").First(context.Background())
	require.NoError(t, err)
	js := env.s.oauthFieldViews(context.Background(), &user, &env.device, &env.app,
		env.schema(env.provider.authURL(), env.provider.tokenURL()))
	var views map[string]oauthFieldView
	require.NoError(t, json.Unmarshal([]byte(js), &views))
	return views
}

func TestOAuthFieldViews(t *testing.T) {
	env := newOAuthTestEnv(t)
	require.NoError(t, env.s.DB.Create(&data.User{Username: "bob", APIKey: "bob-key"}).Error)
	own := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())
	shared := env.createClient(t, "bob", true, env.provider.authURL(), env.provider.tokenURL())
	env.createClient(t, "bob", false, env.provider.authURL(), env.provider.tokenURL())
	env.createClient(t, "alice", false, env.provider.authURL(), "https://other.example/token")

	views := env.configPageOAuthFields(t)

	legacy := views["legacy"]
	assert.False(t, legacy.ServerManaged)

	auth := views["auth"]
	assert.True(t, auth.ServerManaged)
	assert.Nil(t, auth.Connected)
	ids := []string{}
	for _, c := range auth.Clients {
		ids = append(ids, c.ID)
	}
	assert.ElementsMatch(t, []string{own.ID, shared.ID}, ids)
	assert.Equal(t, "/devices/dev1/100/oauth/auth/connect", auth.ConnectURL)
	newClient, err := url.Parse(auth.NewClientURL)
	require.NoError(t, err)
	assert.Equal(t, "/settings/oauth", newClient.Path)
	assert.Equal(t, env.provider.tokenURL(), newClient.Query().Get("token_endpoint"))
	assert.Equal(t, "/devices/dev1/100/config", newClient.Query().Get("return_to"))

	require.Equal(t, http.StatusSeeOther, env.signIn(t, own).Code)
	auth = env.configPageOAuthFields(t)["auth"]
	require.NotNil(t, auth.Connected)
	assert.Equal(t, own.Name, auth.Connected.ClientName)
	assert.False(t, auth.Connected.Mismatch)

	// The app moves to other endpoints: the old connection no longer matches.
	env.s.schemaLoader = nil
	moved := env.schema(env.provider.authURL(), "https://moved.example/token")
	user, _ := gorm.G[data.User](env.s.DB).Where("username = ?", "alice").First(context.Background())
	var movedViews map[string]oauthFieldView
	require.NoError(t, json.Unmarshal([]byte(env.s.oauthFieldViews(context.Background(), &user, &env.device, &env.app, moved)), &movedViews))
	require.NotNil(t, movedViews["auth"].Connected)
	assert.True(t, movedViews["auth"].Connected.Mismatch)
}

func TestOAuthClientSettingsCreateAndEdit(t *testing.T) {
	env := newOAuthTestEnv(t)
	ctx := context.Background()

	rr := env.do(t, http.MethodGet, "/settings/oauth?name=GitHub&authorization_endpoint="+url.QueryEscape("https://github.com/login/oauth/authorize")+"&return_to=/devices/dev1/100/config", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, `value="GitHub"`)
	assert.Contains(t, body, `value="https://github.com/login/oauth/authorize"`)
	assert.Contains(t, body, "http://example.com/oauth/callback/")

	id := "0123456789abcdef"
	form := url.Values{
		"id":                     {id},
		"name":                   {"GitHub"},
		"authorization_endpoint": {"https://GitHub.com/login/oauth/authorize/"},
		"token_endpoint":         {"https://github.com:443/login/oauth/access_token"},
		"client_id":              {"gh-id"},
		"client_secret":          {"gh-secret"},
		"shared":                 {"on"},
		"return_to":              {"/devices/dev1/100/config"},
	}
	rr = env.do(t, http.MethodPost, "/settings/oauth/clients", form)
	require.Equal(t, http.StatusSeeOther, rr.Code, rr.Body.String())
	assert.Equal(t, "/devices/dev1/100/config", rr.Header().Get("Location"))

	client, err := gorm.G[data.OAuthClient](env.s.DB).Where("id = ?", id).First(ctx)
	require.NoError(t, err)
	assert.Equal(t, "alice", client.OwnerUsername)
	assert.False(t, client.Shared, "only admins can share a client")
	assert.Equal(t, "https://github.com/login/oauth/authorize", client.AuthorizationEndpoint)
	assert.Equal(t, "https://github.com/login/oauth/access_token", client.TokenEndpoint)
	assert.Equal(t, "https://github.com/login/oauth/authorize https://github.com/login/oauth/access_token", client.IssuerKey)
	assert.Equal(t, data.Secret("gh-secret"), client.ClientSecret)

	// The secret is write-only: it is never rendered, and an empty field keeps it.
	rr = env.do(t, http.MethodGet, "/settings/oauth?edit="+id, nil)
	require.Equal(t, http.StatusOK, rr.Code)
	assert.NotContains(t, rr.Body.String(), "gh-secret")
	assert.Contains(t, rr.Body.String(), `value="gh-id"`)

	form.Set("name", "GitHub (renamed)")
	form.Set("client_secret", "")
	rr = env.do(t, http.MethodPost, "/settings/oauth/clients", form)
	require.Equal(t, http.StatusSeeOther, rr.Code)
	client, err = gorm.G[data.OAuthClient](env.s.DB).Where("id = ?", id).First(ctx)
	require.NoError(t, err)
	assert.Equal(t, "GitHub (renamed)", client.Name)
	assert.Equal(t, data.Secret("gh-secret"), client.ClientSecret)
}

func TestOAuthClientSettingsValidation(t *testing.T) {
	env := newOAuthTestEnv(t)
	base := url.Values{
		"id":                     {"0123456789abcdef"},
		"name":                   {"X"},
		"authorization_endpoint": {"https://example.com/authorize"},
		"token_endpoint":         {"https://example.com/token"},
		"client_id":              {"id"},
	}
	tests := map[string][2]string{
		"bad id":             {"id", "../etc"},
		"no name":            {"name", ""},
		"no client id":       {"client_id", ""},
		"http token":         {"token_endpoint", "http://example.com/token"},
		"query in authorize": {"authorization_endpoint", "https://example.com/authorize?tenant=x"},
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			form := maps.Clone(base)
			form.Set(change[0], change[1])
			rr := env.do(t, http.MethodPost, "/settings/oauth/clients", form)
			assert.Equal(t, "/settings/oauth", rr.Header().Get("Location"))
			count, err := gorm.G[data.OAuthClient](env.s.DB).Count(context.Background(), "*")
			require.NoError(t, err)
			assert.Zero(t, count)
		})
	}
}

func TestOAuthClientSettingsPermissions(t *testing.T) {
	env := newOAuthTestEnv(t)
	ctx := context.Background()
	require.NoError(t, env.s.DB.Create(&data.User{Username: "admin", APIKey: "admin-key", IsAdmin: true}).Error)
	require.NoError(t, env.s.DB.Create(&data.User{Username: "bob", APIKey: "bob-key"}).Error)
	bobs := env.createClient(t, "bob", false, "https://example.com/authorize", "https://example.com/token")
	shared := env.createClient(t, "admin", true, "https://example.com/authorize", "https://example.com/token")

	// alice can neither edit nor delete bob's client, nor the admin's shared one.
	for _, c := range []data.OAuthClient{bobs, shared} {
		rr := env.do(t, http.MethodPost, "/settings/oauth/clients/"+c.ID+"/delete", url.Values{})
		assert.Equal(t, http.StatusForbidden, rr.Code)
		rr = env.do(t, http.MethodPost, "/settings/oauth/clients", url.Values{
			"id": {c.ID}, "name": {"mine now"}, "client_id": {"x"},
			"authorization_endpoint": {"https://example.com/authorize"},
			"token_endpoint":         {"https://example.com/token"},
		})
		assert.Equal(t, http.StatusForbidden, rr.Code)
	}

	// alice sees the shared client, but not bob's private one.
	rr := env.do(t, http.MethodGet, "/settings/oauth", nil)
	assert.Contains(t, rr.Body.String(), shared.Name)
	assert.NotContains(t, rr.Body.String(), bobs.Name)

	// Any admin may delete a shared client.
	env.login(t, "admin")
	rr = env.do(t, http.MethodPost, "/settings/oauth/clients/"+shared.ID+"/delete", url.Values{})
	assert.Equal(t, http.StatusSeeOther, rr.Code)
	_, err := gorm.G[data.OAuthClient](env.s.DB).Where("id = ?", shared.ID).First(ctx)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestOAuthClientChangesDropTokens(t *testing.T) {
	env := newOAuthTestEnv(t)
	ctx := context.Background()
	require.NoError(t, env.s.DB.Create(&data.User{Username: "admin", APIKey: "admin-key", IsAdmin: true}).Error)
	shared := env.createClient(t, "admin", true, env.provider.authURL(), env.provider.tokenURL())
	require.Equal(t, http.StatusSeeOther, env.signIn(t, shared).Code)
	require.Len(t, env.tokens(t), 1)

	form := url.Values{
		"id": {shared.ID}, "name": {shared.Name}, "client_id": {"cid"},
		"authorization_endpoint": {env.provider.authURL()},
		"token_endpoint":         {env.provider.tokenURL()},
		"shared":                 {"on"},
	}
	env.login(t, "admin")

	// Renaming keeps connections.
	form.Set("name", "Renamed")
	require.Equal(t, http.StatusSeeOther, env.do(t, http.MethodPost, "/settings/oauth/clients", form).Code)
	assert.Len(t, env.tokens(t), 1)

	// Unsharing disconnects other users' apps.
	form.Del("shared")
	require.Equal(t, http.StatusSeeOther, env.do(t, http.MethodPost, "/settings/oauth/clients", form).Code)
	assert.Empty(t, env.tokens(t))

	// Moving a client to other endpoints drops its tokens.
	form.Set("shared", "on")
	require.Equal(t, http.StatusSeeOther, env.do(t, http.MethodPost, "/settings/oauth/clients", form).Code)
	env.login(t, "alice")
	require.Equal(t, http.StatusSeeOther, env.signIn(t, shared).Code)
	require.Len(t, env.tokens(t), 1)
	env.login(t, "admin")
	form.Set("token_endpoint", "https://moved.example/token")
	require.Equal(t, http.StatusSeeOther, env.do(t, http.MethodPost, "/settings/oauth/clients", form).Code)
	assert.Empty(t, env.tokens(t))

	client, err := gorm.G[data.OAuthClient](env.s.DB).Where("id = ?", shared.ID).First(ctx)
	require.NoError(t, err)
	assert.Equal(t, "https://moved.example/token", client.TokenEndpoint)
}

func TestOAuthTokensDeletedWithApp(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())
	require.Equal(t, http.StatusSeeOther, env.signIn(t, client).Code)

	rr := env.do(t, http.MethodPost, "/devices/dev1/100/delete", url.Values{})
	require.Less(t, rr.Code, 400, rr.Body.String())
	assert.Empty(t, env.tokens(t))
}

func TestOAuthTokensDeletedWithDevice(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())
	require.Equal(t, http.StatusSeeOther, env.signIn(t, client).Code)

	rr := env.do(t, http.MethodPost, "/devices/dev1/delete", url.Values{})
	require.Less(t, rr.Code, 400, rr.Body.String())
	assert.Empty(t, env.tokens(t))
}

func TestOAuthClientsDeletedWithUser(t *testing.T) {
	env := newOAuthTestEnv(t)
	ctx := context.Background()
	require.NoError(t, env.s.DB.Create(&data.User{Username: "admin", APIKey: "admin-key", IsAdmin: true}).Error)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())
	require.Equal(t, http.StatusSeeOther, env.signIn(t, client).Code)

	env.login(t, "admin")
	rr := env.do(t, http.MethodDelete, "/settings/admin/users/alice", nil)
	require.Less(t, rr.Code, 400, rr.Body.String())

	count, err := gorm.G[data.OAuthClient](env.s.DB).Count(ctx, "*")
	require.NoError(t, err)
	assert.Zero(t, count)
	assert.Empty(t, env.tokens(t))
}

func TestConfigPageRendersOAuthFields(t *testing.T) {
	env := newOAuthTestEnv(t)
	client := env.createClient(t, "alice", false, env.provider.authURL(), env.provider.tokenURL())

	rr := env.do(t, http.MethodGet, "/devices/dev1/100/config", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	body := rr.Body.String()
	assert.Contains(t, body, "const oauthFields = {")
	assert.Contains(t, body, client.ID)
	assert.Contains(t, body, `case "oauth2":`)
	assert.NotContains(t, body, "csecret")
}
