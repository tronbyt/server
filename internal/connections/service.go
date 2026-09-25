package connections

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"tronbyt-server/internal/data"

	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

// Service is the high-level façade the HTTP handlers and renderer call.
//
// It reads provider client credentials via GetCreds, persists encrypted
// tokens, and refreshes them on demand. The encryption secret is the
// server's session secret_key (already used to sign session cookies);
// the same key the rest of the server treats as load-bearing.
type Service struct {
	DB       *gorm.DB
	Registry *Registry
	Secret   string          // session secret_key for token encryption
	GetCreds CredentialsFunc // admin-provided client id/secret per provider

	// refreshMu guards refreshLocks; refreshLocks single-flights token
	// refreshes per connection. Providers like Strava rotate the refresh
	// token on every use (the old one dies immediately), so two renders
	// refreshing concurrently must serialize or a lost race can persist a
	// dead refresh token.
	refreshMu    sync.Mutex
	refreshLocks map[uint]*sync.Mutex
}

// connectionLock returns the per-connection mutex, creating it on first use.
func (s *Service) connectionLock(id uint) *sync.Mutex {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.refreshLocks == nil {
		s.refreshLocks = make(map[uint]*sync.Mutex)
	}
	m, ok := s.refreshLocks[id]
	if !ok {
		m = &sync.Mutex{}
		s.refreshLocks[id] = m
	}
	return m
}

// CredentialsFunc returns the OAuth client_id / client_secret for a provider,
// typically from server config / env vars. Returning ("", "") means the
// admin hasn't enabled this provider — handlers should treat that as
// ErrProviderDisabled.
type CredentialsFunc func(provider string) (clientID, clientSecret string)

// Errors surfaced to the HTTP layer.
var (
	ErrConnectionNotFound = errors.New("connections: not found")
)

// ExchangeCode is called from the OAuth callback. It exchanges the
// authorization code for tokens, optionally identifies the user against the
// provider, then upserts the Connection row for this (user, provider).
func (s *Service) ExchangeCode(ctx context.Context, providerName, username, redirectURL, code string, requestedScopes []string) (*data.Connection, error) {
	provider, ok := s.Registry.Get(providerName)
	if !ok {
		return nil, fmt.Errorf("connections: unknown provider %q", providerName)
	}

	clientID, clientSecret := s.GetCreds(provider.Name)
	if clientID == "" || clientSecret == "" {
		return nil, ErrProviderDisabled
	}

	cfg := provider.OAuth2Config(clientID, clientSecret, redirectURL, requestedScopes)
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("connections: token exchange: %w", err)
	}

	externalID, displayName := "", ""
	if provider.Identify != nil && tok.AccessToken != "" {
		if eid, dn, err := provider.Identify(ctx, tok.AccessToken); err != nil {
			slog.Warn("connections: identify failed, continuing", "provider", provider.Name, "error", err)
		} else {
			externalID, displayName = eid, dn
		}
	}

	return s.upsertConnection(ctx, provider, username, tok, externalID, displayName, requestedScopes)
}

// upsertConnection writes (or replaces) the user's Connection for this
// provider and returns the persisted row.
func (s *Service) upsertConnection(ctx context.Context, provider *Provider, username string, tok *oauth2.Token, externalID, displayName string, scopes []string) (*data.Connection, error) {
	sealedAccess, err := Seal(s.Secret, []byte(tok.AccessToken))
	if err != nil {
		return nil, fmt.Errorf("connections: seal access token: %w", err)
	}
	sealedRefresh, err := Seal(s.Secret, []byte(tok.RefreshToken))
	if err != nil {
		return nil, fmt.Errorf("connections: seal refresh token: %w", err)
	}

	// Prefer the scopes the provider says it GRANTED over the ones we
	// asked for: consent screens let users uncheck individual scopes
	// (Strava's does), so the requested set is not what the token can
	// actually do. Fall back to the request only when the provider
	// reports nothing.
	scopeStr := ""
	if v, ok := tok.Extra("scope").(string); ok && v != "" {
		scopeStr = strings.Join(strings.FieldsFunc(v, func(r rune) bool {
			return r == ',' || r == ' '
		}), " ")
	}
	if scopeStr == "" {
		scopeStr = strings.Join(scopes, " ")
	}

	existing, err := gorm.G[data.Connection](s.DB).
		Where("user_id = ? AND provider = ?", username, provider.Name).
		First(ctx)

	conn := data.Connection{
		UserID:          username,
		Provider:        provider.Name,
		ExternalID:      externalID,
		DisplayName:     displayName,
		Scopes:          scopeStr,
		AccessToken:     sealedAccess,
		RefreshToken:    sealedRefresh,
		AccessExpiresAt: tok.Expiry,
	}

	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := gorm.G[data.Connection](s.DB).Create(ctx, &conn); err != nil {
			return nil, fmt.Errorf("connections: create: %w", err)
		}
		return &conn, nil
	case err != nil:
		return nil, fmt.Errorf("connections: lookup: %w", err)
	default:
		conn.ID = existing.ID
		conn.CreatedAt = existing.CreatedAt
		// If the provider didn't echo a fresh refresh token (some don't on
		// every exchange), keep the existing one.
		if tok.RefreshToken == "" && len(existing.RefreshToken) > 0 {
			conn.RefreshToken = existing.RefreshToken
		}
		// A re-connect where Identify failed (or granted-scope reporting was
		// absent) must not clobber the identity we already know.
		if conn.ExternalID == "" {
			conn.ExternalID = existing.ExternalID
		}
		if conn.DisplayName == "" {
			conn.DisplayName = existing.DisplayName
		}
		if conn.Scopes == "" {
			conn.Scopes = existing.Scopes
		}
		if err := s.DB.Save(&conn).Error; err != nil {
			return nil, fmt.Errorf("connections: update: %w", err)
		}
		return &conn, nil
	}
}

// AccessTokenForUser returns a usable access token for (user, provider),
// transparently refreshing it if it has expired or is close to expiry. It
// returns ErrConnectionNotFound if the user has not connected this
// provider.
func (s *Service) AccessTokenForUser(ctx context.Context, providerName, username string) (string, error) {
	provider, ok := s.Registry.Get(providerName)
	if !ok {
		return "", fmt.Errorf("connections: unknown provider %q", providerName)
	}

	conn, err := gorm.G[data.Connection](s.DB).
		Where("user_id = ? AND provider = ?", username, providerName).
		First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrConnectionNotFound
	}
	if err != nil {
		return "", err
	}

	access, err := Open(s.Secret, conn.AccessToken)
	if err != nil {
		return "", fmt.Errorf("connections: open access token: %w", err)
	}

	if staleToken(access, conn.AccessExpiresAt) {
		refreshed, rerr := s.refresh(ctx, provider, &conn)
		if rerr != nil {
			return "", rerr
		}
		return refreshed, nil
	}
	return string(access), nil
}

// staleToken reports whether the stored access token needs a refresh:
// missing, expired, or expiring within 60 seconds (so a render that takes a
// few seconds doesn't get a 401). A zero expiry follows the x/oauth2
// convention — the token never expires — because providers that omit
// expires_in typically also issue no refresh token, and treating zero as
// "expired" would refresh (and fail) on every render forever.
func staleToken(access []byte, expiresAt time.Time) bool {
	if len(access) == 0 {
		return true
	}
	return !expiresAt.IsZero() && time.Until(expiresAt) < 60*time.Second
}

// refreshCreds returns the credentials to use for a refresh grant. A
// public client connected by device flow has no secret configured, and
// refreshing such a token doesn't need one — requiring a secret here
// would break the connection the first time its access token expired
// (GitHub now issues expiring tokens to new OAuth apps by default).
func (s *Service) refreshCreds(provider *Provider) (clientID, clientSecret string, ok bool) {
	clientID, clientSecret = s.GetCreds(provider.Name)
	if clientID == "" {
		return "", "", false
	}
	publicClient := provider.SupportsDeviceAuth() && !provider.DeviceAuthNeedsSecret
	if clientSecret == "" && !publicClient {
		return "", "", false
	}
	return clientID, clientSecret, true
}

// refresh performs a refresh-token exchange and persists the new tokens.
// It single-flights per connection: with rotating-refresh-token providers
// (Strava), concurrent refreshes are a correctness hazard, not just wasted
// work. The loser of the race re-reads the row and returns the winner's
// fresh token instead of spending the (now dead) old refresh token.
func (s *Service) refresh(ctx context.Context, provider *Provider, conn *data.Connection) (string, error) {
	lock := s.connectionLock(conn.ID)
	lock.Lock()
	defer lock.Unlock()

	// Re-read under the lock: another goroutine may have refreshed while we
	// waited, in which case the stored token is already fresh.
	if current, err := gorm.G[data.Connection](s.DB).
		Where("id = ?", conn.ID).
		First(ctx); err == nil {
		access, aerr := Open(s.Secret, current.AccessToken)
		if aerr == nil && !staleToken(access, current.AccessExpiresAt) {
			return string(access), nil
		}
		conn = &current
	}

	refresh, err := Open(s.Secret, conn.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("connections: open refresh token: %w", err)
	}
	if len(refresh) == 0 {
		return "", fmt.Errorf("connections: no refresh token stored, reconnect required")
	}

	clientID, clientSecret, ok := s.refreshCreds(provider)
	if !ok {
		return "", ErrProviderDisabled
	}

	cfg := provider.OAuth2Config(clientID, clientSecret, "", nil)
	src := cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: string(refresh)})
	tok, err := src.Token()
	if err != nil {
		return "", fmt.Errorf("connections: refresh: %w", err)
	}

	sealedAccess, err := Seal(s.Secret, []byte(tok.AccessToken))
	if err != nil {
		return "", err
	}
	updates := data.Connection{
		ID:              conn.ID,
		AccessToken:     sealedAccess,
		AccessExpiresAt: tok.Expiry,
	}
	q := gorm.G[data.Connection](s.DB).Where("id = ?", conn.ID)
	if tok.RefreshToken != "" && tok.RefreshToken != string(refresh) {
		sealedRefresh, err := Seal(s.Secret, []byte(tok.RefreshToken))
		if err != nil {
			return "", err
		}
		updates.RefreshToken = sealedRefresh
		q = q.Select("AccessToken", "AccessExpiresAt", "RefreshToken")
	} else {
		q = q.Select("AccessToken", "AccessExpiresAt")
	}
	if _, err := q.Updates(ctx, updates); err != nil {
		return "", fmt.Errorf("connections: persist refreshed token: %w", err)
	}
	return tok.AccessToken, nil
}

// CodeFlowAvailable reports whether the admin has supplied what the
// browser-redirect flow needs for this provider (id *and* secret).
func (s *Service) CodeFlowAvailable(provider *Provider) bool {
	if !provider.SupportsCodeFlow() {
		return false
	}
	clientID, clientSecret := s.GetCreds(provider.Name)
	return clientID != "" && clientSecret != ""
}

// DeviceFlowAvailable reports whether the device authorization grant can
// run for this provider. Public-client providers need only a client id,
// so an admin can enable GitHub with a single env var.
func (s *Service) DeviceFlowAvailable(provider *Provider) bool {
	if !provider.SupportsDeviceAuth() {
		return false
	}
	clientID, clientSecret := s.GetCreds(provider.Name)
	if clientID == "" {
		return false
	}
	return clientSecret != "" || !provider.DeviceAuthNeedsSecret
}

// deviceConfig builds the oauth2 config for a device-flow exchange. The
// client secret is withheld from providers that don't want one, so a
// public-client exchange stays a public-client exchange.
//
// AuthStyle is forced to AuthStyleInParams. The library's auto-detect
// would send Basic auth with an empty password for a secretless client,
// and — because it only caches the style on success, while every pending
// poll is an "error" — it would probe both styles on *every* tick,
// doubling the request rate against the provider's rate limit.
func (s *Service) deviceConfig(provider *Provider, scopes []string) *oauth2.Config {
	clientID, clientSecret := s.GetCreds(provider.Name)
	if !provider.DeviceAuthNeedsSecret {
		clientSecret = ""
	}
	cfg := provider.OAuth2Config(clientID, clientSecret, "", scopes)
	cfg.Endpoint.AuthStyle = oauth2.AuthStyleInParams
	return cfg
}

// StartDeviceAuth requests a user code from the provider. It returns
// immediately — the returned response carries the code to show the user,
// the URL they visit, and how long they have. Polling happens separately
// in CompleteDeviceAuth.
func (s *Service) StartDeviceAuth(ctx context.Context, providerName string, scopes []string) (*Provider, *oauth2.DeviceAuthResponse, error) {
	provider, ok := s.Registry.Get(providerName)
	if !ok {
		return nil, nil, fmt.Errorf("connections: unknown provider %q", providerName)
	}
	if !s.DeviceFlowAvailable(provider) {
		return nil, nil, ErrProviderDisabled
	}

	resp, err := s.deviceConfig(provider, scopes).DeviceAuth(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("connections: device authorization request: %w", err)
	}
	return provider, resp, nil
}

// CompleteDeviceAuth polls the provider's token endpoint until the user
// approves, denies, or the code expires, then persists the connection.
// It BLOCKS for as long as the code is valid (typically 5–15 minutes),
// so callers run it in a goroutine and report progress out of band.
func (s *Service) CompleteDeviceAuth(ctx context.Context, providerName, username string, da *oauth2.DeviceAuthResponse, scopes []string) (*data.Connection, error) {
	provider, ok := s.Registry.Get(providerName)
	if !ok {
		return nil, fmt.Errorf("connections: unknown provider %q", providerName)
	}
	if !s.DeviceFlowAvailable(provider) {
		return nil, ErrProviderDisabled
	}

	tok, err := s.deviceConfig(provider, scopes).DeviceAccessToken(ctx, da)
	if err != nil {
		return nil, fmt.Errorf("connections: device token exchange: %w", err)
	}

	externalID, displayName := "", ""
	if provider.Identify != nil && tok.AccessToken != "" {
		if eid, dn, err := provider.Identify(ctx, tok.AccessToken); err != nil {
			slog.Warn("connections: identify failed, continuing", "provider", provider.Name, "error", err)
		} else {
			externalID, displayName = eid, dn
		}
	}

	return s.upsertConnection(ctx, provider, username, tok, externalID, displayName, scopes)
}

// ListForUser returns all of a user's connections, sorted by provider.
func (s *Service) ListForUser(ctx context.Context, username string) ([]data.Connection, error) {
	conns, err := gorm.G[data.Connection](s.DB).
		Where("user_id = ?", username).
		Order("provider ASC").
		Find(ctx)
	if err != nil {
		return nil, err
	}
	return conns, nil
}

// Disconnect deletes a connection. The caller must verify ownership before
// calling.
func (s *Service) Disconnect(ctx context.Context, id uint, username string) error {
	rows, err := gorm.G[data.Connection](s.DB).
		Where("id = ? AND user_id = ?", id, username).
		Delete(ctx)
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrConnectionNotFound
	}
	return nil
}
