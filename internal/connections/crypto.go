// Package connections implements the third-party OAuth2 connection store
// that lets pixlet apps (Strava, Spotify, Google Calendar, ...) render with
// a fresh access token without users hand-pasting refresh tokens.
package connections

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

// deriveKey turns the server's session secret_key (any length) into a stable
// 32-byte AES-256 key. We use a fixed info string so rotating session keys
// (which would also rotate this key) intentionally invalidates stored tokens.
func deriveKey(secret string) [32]byte {
	return sha256.Sum256([]byte("tronbyt-connections-v1\x00" + secret))
}

// Seal encrypts plaintext with AES-256-GCM. Output layout: nonce || ciphertext.
func Seal(secret string, plaintext []byte) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, nil
	}
	key := deriveKey(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal. Empty input returns empty output (treated as "not set").
func Open(secret string, sealed []byte) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, nil
	}
	key := deriveKey(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, errors.New("connections: sealed token too short")
	}
	nonce, ct := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}
