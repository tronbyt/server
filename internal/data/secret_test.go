package data

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretScan(t *testing.T) {
	var s Secret
	require.NoError(t, s.Scan("token"))
	assert.Equal(t, Secret("token"), s)
	require.NoError(t, s.Scan([]byte("bytes")))
	assert.Equal(t, Secret("bytes"), s)
	require.NoError(t, s.Scan(nil))
	assert.Equal(t, Secret(""), s)

	assert.Error(t, s.Scan("enc:v1:abcdef"), "ciphertext from a newer version is refused")
	assert.Error(t, s.Scan(42))

	v, err := Secret("token").Value()
	require.NoError(t, err)
	assert.Equal(t, "token", v)
}
