package connections

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Provider describes one third-party OAuth2 provider.
//
// Each provider declares its OAuth2 endpoints, the env-var names that hold
// its credentials (the *server admin* registers their own OAuth app and
// supplies these), and an optional fetcher for the provider's user-id /
// display name. The fetcher runs once at connection time so we can show a
// useful label like "athlete #1234567" in the UI.
type Provider struct {
	// Name is the stable identifier used in URLs and DB rows: "strava".
	Name string

	// DisplayName is shown in the UI: "Strava".
	DisplayName string

	// AuthorizeURL / TokenURL are the provider's OAuth2 endpoints. AuthorizeURL
	// is also matched (by host) when we look at a pixlet schema.OAuth2 field
	// to decide which provider it refers to.
	AuthorizeURL string
	TokenURL     string

	// AuthStyle controls how client credentials reach the token endpoint.
	// The zero value (AutoDetect) probes header-then-params and caches the
	// winner; set it explicitly when the provider documents one style
	// (Strava: params; Spotify: Basic header).
	AuthStyle oauth2.AuthStyle

	// ScopeJoin, when non-empty, collapses the scope list into a single
	// element joined by this separator before the authorize redirect.
	// Strava wants "read,activity:read" — one comma-joined value — while
	// the oauth2 library would otherwise join multiple scopes with spaces.
	ScopeJoin string

	// DeviceAuthURL enables the RFC 8628 device authorization grant: the
	// user is shown a short code (on the web page and on the display
	// itself) and approves it on a phone or laptop. No redirect URI is
	// involved, which makes it the natural fit for a device on a shelf.
	// Empty means the provider offers no device flow.
	DeviceAuthURL string

	// DeviceAuthNeedsSecret marks providers that still require the client
	// secret at the device-flow token exchange (Trakt, Google). True
	// public clients (GitHub, Microsoft) need only a client id, so the
	// admin can enable them without configuring a secret at all.
	DeviceAuthNeedsSecret bool

	// DefaultScopes is used when the schema does not declare scopes.
	DefaultScopes []string

	// Identify is called after a successful token exchange to fetch a
	// stable provider-side user id (and optional display name). It receives
	// a fresh access token. Optional — if nil, ExternalID stays empty.
	Identify func(ctx context.Context, accessToken string) (externalID, displayName string, err error)
}

// OAuth2Config builds a *oauth2.Config tied to a specific runtime redirect URI.
// Caller supplies clientID/clientSecret (loaded from env per provider).
func (p *Provider) OAuth2Config(clientID, clientSecret, redirectURL string, scopes []string) *oauth2.Config {
	if len(scopes) == 0 {
		scopes = p.DefaultScopes
	}
	if p.ScopeJoin != "" && len(scopes) > 1 {
		scopes = []string{strings.Join(scopes, p.ScopeJoin)}
	}
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       scopes,
		Endpoint: oauth2.Endpoint{
			AuthURL:       p.AuthorizeURL,
			TokenURL:      p.TokenURL,
			DeviceAuthURL: p.DeviceAuthURL,
			AuthStyle:     p.AuthStyle,
		},
	}
}

// SupportsDeviceAuth reports whether this provider offers the device
// authorization grant.
func (p *Provider) SupportsDeviceAuth() bool {
	return p.DeviceAuthURL != ""
}

// SupportsCodeFlow reports whether this provider offers the browser
// redirect (authorization code) flow. A device-flow-only provider — a
// public client with no registered redirect URI — sets AuthorizeURL
// empty.
func (p *Provider) SupportsCodeFlow() bool {
	return p.AuthorizeURL != ""
}

// Registry indexes providers by name and looks them up by an
// authorization_endpoint URL declared in a pixlet schema.OAuth2 field.
type Registry struct {
	byName map[string]*Provider
	byHost map[string]*Provider
}

// NewRegistry builds a registry from the given providers.
func NewRegistry(providers ...*Provider) *Registry {
	r := &Registry{
		byName: make(map[string]*Provider, len(providers)),
		byHost: make(map[string]*Provider, len(providers)),
	}
	for _, p := range providers {
		r.byName[p.Name] = p
		if u, err := url.Parse(p.AuthorizeURL); err == nil && u.Host != "" {
			r.byHost[strings.ToLower(u.Host)] = p
		}
	}
	return r
}

// Get looks up a provider by its stable name (e.g. "strava").
func (r *Registry) Get(name string) (*Provider, bool) {
	p, ok := r.byName[strings.ToLower(name)]
	return p, ok
}

// All returns providers in registration order is not guaranteed; use Names() for a
// deterministic listing if needed.
func (r *Registry) All() []*Provider {
	out := make([]*Provider, 0, len(r.byName))
	for _, p := range r.byName {
		out = append(out, p)
	}
	return out
}

// MatchAuthorizeURL returns the provider whose AuthorizeURL host matches the
// given URL. This is how we decide which provider a pixlet schema.OAuth2
// field refers to without modifying the field shape.
func (r *Registry) MatchAuthorizeURL(rawURL string) (*Provider, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return nil, false
	}
	p, ok := r.byHost[strings.ToLower(u.Host)]
	return p, ok
}

// ErrProviderDisabled is returned when the admin hasn't supplied client
// credentials for a provider.
var ErrProviderDisabled = errors.New("connections: provider not configured (missing client id/secret)")

// Token represents a freshly-exchanged or refreshed OAuth2 token plus any
// provider-supplied athlete/external id.
type Token struct {
	AccessToken  string
	RefreshToken string
	Expiry       time.Time
	Scopes       []string

	ExternalID  string
	DisplayName string
}

// formatExternalID exists so providers can produce a friendly default for
// DisplayName when their API doesn't return a name.
func formatExternalID(provider, id string) string {
	if id == "" {
		return ""
	}
	return fmt.Sprintf("%s #%s", provider, id)
}
