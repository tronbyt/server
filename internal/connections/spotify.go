package connections

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

// Spotify returns the Spotify OAuth2 provider definition. Endpoints from
// https://developer.spotify.com/documentation/web-api/tutorials/code-flow.
// Spotify authenticates the token endpoint with an HTTP Basic header,
// refresh responses may omit the refresh_token (the stored one stays
// valid), and access tokens live one hour.
//
// Note for admins: since late 2025 Spotify requires HTTPS redirect URIs
// (plain http is allowed only for literal loopback addresses like
// http://127.0.0.1:8000 — "localhost", LAN IPs, and .local names are
// rejected), and Development Mode apps are capped at 5 allowlisted users
// with a Premium-subscribed app owner. See .env.example.
func Spotify() *Provider {
	return &Provider{
		Name:         "spotify",
		DisplayName:  "Spotify",
		AuthorizeURL: "https://accounts.spotify.com/authorize",
		TokenURL:     "https://accounts.spotify.com/api/token",
		AuthStyle:    oauth2.AuthStyleInHeader,
		DefaultScopes: []string{
			"user-read-currently-playing",
			"user-read-playback-state",
			"user-read-recently-played",
		},
		Identify: identifySpotify,
	}
}

// identifySpotify calls /v1/me to capture the user id and display name.
// The basic profile fields need no extra scopes.
func identifySpotify(ctx context.Context, accessToken string) (externalID, displayName string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.spotify.com/v1/me", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("spotify /v1/me returned %d", resp.StatusCode)
	}

	var body struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", err
	}

	externalID = body.ID
	displayName = body.DisplayName
	if displayName == "" {
		displayName = formatExternalID("spotify", externalID)
	}
	return externalID, displayName, nil
}
