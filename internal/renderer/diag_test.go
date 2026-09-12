//go:build diagnostic

package renderer

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Run with: go test -tags diagnostic ./internal/renderer/ -run TestDiag -v
// Diagnostic helper for the OAuth2-injection investigation. Not part of CI.
func TestDiagOAuthRender(t *testing.T) {
	path := "/tmp/tronbyt-test/users/admin/apps/oauth-test/oauth-test.star"
	cfg := map[string]any{"strava": "TEST-TOKEN-1234567890"}
	tz := "America/New_York"
	_, msgs, err := Render(
		context.Background(), path, cfg, 64, 32,
		15*time.Second, 30*time.Second, false, false,
		&tz, nil, nil, nil,
	)
	for _, m := range msgs {
		fmt.Println("msg:", m)
	}
	if err != nil {
		t.Fatal(err)
	}
}
