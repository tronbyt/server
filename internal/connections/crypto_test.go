package connections

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSealOpenRoundtrip(t *testing.T) {
	secret := "this-is-a-test-secret"
	plaintext := []byte("strava-refresh-token-1234567890")

	sealed, err := Seal(secret, plaintext)
	require.NoError(t, err)
	require.NotEmpty(t, sealed)
	assert.False(t, bytes.Equal(sealed, plaintext), "sealed output should differ from plaintext")

	opened, err := Open(secret, sealed)
	require.NoError(t, err)
	assert.Equal(t, plaintext, opened)
}

func TestSealNonDeterministic(t *testing.T) {
	// AES-GCM uses a random nonce, so sealing the same plaintext twice
	// must produce different ciphertexts. This guards against ECB-style
	// regressions if anyone "simplifies" the implementation later.
	secret := "secret"
	plaintext := []byte("same-plaintext-each-time")

	a, err := Seal(secret, plaintext)
	require.NoError(t, err)
	b, err := Seal(secret, plaintext)
	require.NoError(t, err)
	assert.False(t, bytes.Equal(a, b), "two seals of the same plaintext must differ")
}

func TestOpenWithWrongKey(t *testing.T) {
	sealed, err := Seal("right-secret", []byte("payload"))
	require.NoError(t, err)

	_, err = Open("wrong-secret", sealed)
	assert.Error(t, err, "opening with the wrong key must fail")
}

func TestSealOpenEmpty(t *testing.T) {
	// Empty input is a legitimate "not set" state — round-trip without
	// error so the service can store/clear tokens uniformly.
	sealed, err := Seal("secret", nil)
	require.NoError(t, err)
	assert.Empty(t, sealed)

	opened, err := Open("secret", nil)
	require.NoError(t, err)
	assert.Empty(t, opened)
}

func TestOpenTooShort(t *testing.T) {
	_, err := Open("secret", []byte{1, 2, 3})
	assert.Error(t, err)
}
