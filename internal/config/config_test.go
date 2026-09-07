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
