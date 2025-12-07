package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

// VerifyPassword checks the password against the hash.
// It supports bcrypt (native), scrypt (legacy Werkzeug), and pbkdf2 (legacy Werkzeug).
// It returns true if valid, and a bool indicating if the hash is legacy and should be upgraded.
func VerifyPassword(hashStr, password string) (bool, bool, error) {
	slog.Info("VerifyPassword called", "hash_len", len(hashStr))
	// 1. Check for Bcrypt (starts with $2a$, $2b$, $2y$)
	if strings.HasPrefix(hashStr, "$2") {
		err := bcrypt.CompareHashAndPassword([]byte(hashStr), []byte(password))
		if err == nil {
			return true, false, nil // Valid and current
		}
		return false, false, err
	}

	// 2. Check for Legacy Werkzeug formats
	if strings.HasPrefix(hashStr, "scrypt:") {
		valid, err := verifyScrypt(hashStr, password)
		return valid, true, err // Valid (if true) but legacy (needs upgrade)
	}

	if strings.HasPrefix(hashStr, "pbkdf2:sha256:") {
		valid, err := verifyPbkdf2(hashStr, password)
		return valid, true, err // Valid (if true) but legacy
	}

	return false, false, errors.New("unknown hash format")
}

// HashPassword generates a bcrypt hash of the password.
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func verifyScrypt(hashStr, password string) (bool, error) {
	slog.Info("verifyScrypt", "hashStr", hashStr)
	// Format: scrypt:N:r:p$salt$hash
	parts := strings.Split(hashStr, "$")
	if len(parts) != 3 {
		slog.Error("verifyScrypt parts mismatch", "count", len(parts), "parts", parts)
		return false, errors.New("invalid scrypt format")
	}

	params := strings.Split(parts[0], ":")
	if len(params) != 4 {
		return false, errors.New("invalid scrypt parameters")
	}

	N, err := strconv.Atoi(params[1])
	if err != nil {
		return false, err
	}
	r, err := strconv.Atoi(params[2])
	if err != nil {
		return false, err
	}
	p, err := strconv.Atoi(params[3])
	if err != nil {
		return false, err
	}

	salt := parts[1]
	hashHex := parts[2]

	decodedHash, err := hex.DecodeString(hashHex)
	if err != nil {
		return false, err
	}

	dk, err := scrypt.Key([]byte(password), []byte(salt), N, r, p, len(decodedHash))
	if err != nil {
		return false, err
	}

	return subtle.ConstantTimeCompare(decodedHash, dk) == 1, nil
}

func verifyPbkdf2(hashStr, password string) (bool, error) {
	// Format: pbkdf2:sha256:iterations$salt$hash
	parts := strings.Split(hashStr, "$")
	if len(parts) != 3 {
		return false, errors.New("invalid pbkdf2 format")
	}

	params := strings.Split(parts[0], ":")
	if len(params) != 3 {
		return false, errors.New("invalid pbkdf2 parameters")
	}

	iterations, err := strconv.Atoi(params[2])
	if err != nil {
		return false, err
	}

	salt := parts[1]
	hashHex := parts[2]

	decodedHash, err := hex.DecodeString(hashHex)
	if err != nil {
		return false, err
	}

	// Werkzeug uses SHA256 for pbkdf2:sha256
	dk := pbkdf2.Key([]byte(password), []byte(salt), iterations, len(decodedHash), sha256.New)

	return subtle.ConstantTimeCompare(decodedHash, dk) == 1, nil
}
