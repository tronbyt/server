package connections

import (
	"golang.org/x/oauth2"
)

// Google returns the Google OAuth2 provider definition. Endpoints from
// https://developers.google.com/identity/protocols/oauth2/web-server.
// Redirect (authorization code) flow only: Google's device flow has a
// small scope allowlist (TV-class scopes like YouTube and Drive) that
// excludes the API scopes apps actually declare — analytics.readonly,
// calendar.readonly, and friends — so DeviceAuthURL stays empty.
//
// Google only issues a refresh token when the authorize redirect carries
// access_type=offline, and only guarantees one on re-auth when
// prompt=consent forces the consent screen. Those two are pinned in
// AuthCodeParams rather than inherited from the default options: the
// default happens to spell the same thing today, but it goes through
// oauth2.ApprovalForce, whose spelling has changed before (it used to be
// approval_prompt=force — a parameter Google now rejects when prompt is
// present). Refresh responses do not rotate the refresh token — the
// omitted-refresh-token path in Service.refresh keeps the stored one.
//
// No Identify: the userinfo endpoint needs an email/profile scope on the
// token, and we request only what the app's schema declares. ExternalID
// stays empty and the UI falls back to the provider display name.
//
// Note for admins: a Cloud project whose OAuth consent screen is in
// "Testing" status issues refresh tokens that EXPIRE AFTER 7 DAYS. Publish
// the consent screen to "In production" for durable tokens — staying
// unverified is fine (users click through a warning screen), it just caps
// the app at 100 lifetime users, which doesn't matter for self-hosting.
// See .env.example.
func Google() *Provider {
	return &Provider{
		Name:         "google",
		DisplayName:  "Google",
		AuthorizeURL: "https://accounts.google.com/o/oauth2/v2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		AuthStyle:    oauth2.AuthStyleInParams,
		AuthCodeParams: []oauth2.AuthCodeOption{
			oauth2.AccessTypeOffline,
			oauth2.SetAuthURLParam("prompt", "consent"),
		},
		DefaultScopes: []string{"https://www.googleapis.com/auth/analytics.readonly"},
	}
}
