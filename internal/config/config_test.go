package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadSettings(t *testing.T) {
	t.Setenv("DATA_DIR", "testdata")
	t.Setenv("OIDC_ADDITIONAL_SCOPES", "groups roles")

	cfg, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, "testdata", cfg.DataDir)
	require.Equal(t, "groups roles", cfg.OIDCAdditionalScopes)
}

// envDefault must not defeat an explicit network opt-out.
func TestNibletURLCanBeDisabled(t *testing.T) {
	t.Setenv("NIBLET_CLOUD_URL", "")
	cfg, err := LoadSettings()
	require.NoError(t, err)
	require.Empty(t, cfg.NibletCloudURL)
}
