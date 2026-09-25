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

// GitHub returns the GitHub provider. GitHub is the marquee device-flow
// provider: the token exchange needs only a client id, so an admin can
// enable it with a single env var and no secret.
//
// Note for admins: "Enable Device Flow" must be ticked on the OAuth app
// (Settings → Developer settings → OAuth Apps); without it the flow
// fails with device_flow_disabled.
func GitHub() *Provider {
	return &Provider{
		Name:                  "github",
		DisplayName:           "GitHub",
		AuthorizeURL:          "https://github.com/login/oauth/authorize",
		TokenURL:              "https://github.com/login/oauth/access_token",
		DeviceAuthURL:         "https://github.com/login/device/code",
		DeviceAuthNeedsSecret: false,
		AuthStyle:             oauth2.AuthStyleInParams,
		DefaultScopes:         []string{"read:user"},
		Identify:              identifyGitHub,
	}
}

// identifyGitHub calls /user for the account id and a display name.
func identifyGitHub(ctx context.Context, accessToken string) (externalID, displayName string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/user", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("github /user returned %d", resp.StatusCode)
	}

	var body struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
		Name  string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", "", err
	}

	externalID = strconv.FormatInt(body.ID, 10)
	switch {
	case body.Login != "":
		displayName = body.Login
	case body.Name != "":
		displayName = body.Name
	default:
		displayName = formatExternalID("github", externalID)
	}
	return externalID, displayName, nil
}
