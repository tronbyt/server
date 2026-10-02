package connections

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/oauth2"
)

// Strava returns the Strava OAuth2 provider definition. Endpoints from
// https://developers.strava.com/docs/authentication/. Strava is a
// confidential-client-only provider (no PKCE, no device flow), wants
// client credentials as POST params, rotates refresh tokens on use, and
// historically required comma-delimited scopes — hence ScopeJoin.
func Strava() *Provider {
	return &Provider{
		Name:          "strava",
		DisplayName:   "Strava",
		AuthorizeURL:  "https://www.strava.com/oauth/authorize",
		TokenURL:      "https://www.strava.com/oauth/token",
		AuthStyle:     oauth2.AuthStyleInParams,
		ScopeJoin:     ",",
		DefaultScopes: []string{"read", "activity:read"},
		Identify:      identifyStrava,
	}
}

// identifyStrava calls /api/v3/athlete to capture the athlete id and a
// display name. We only need this once at connection time; the renderer
// will use the access token directly afterwards.
func identifyStrava(ctx context.Context, accessToken string) (externalID, displayName string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.strava.com/api/v3/athlete", nil)
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
		return "", "", fmt.Errorf("strava /athlete returned %d", resp.StatusCode)
	}

	var body struct {
		ID        int64  `json:"id"`
		FirstName string `json:"firstname"`
		LastName  string `json:"lastname"`
		Username  string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", err
	}

	externalID = strconv.FormatInt(body.ID, 10)
	switch {
	case body.FirstName != "" || body.LastName != "":
		displayName = fmt.Sprintf("%s %s", body.FirstName, body.LastName)
	case body.Username != "":
		displayName = body.Username
	default:
		displayName = formatExternalID("strava", externalID)
	}
	return externalID, displayName, nil
}
