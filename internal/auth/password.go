package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/pbkdf2"
	"golang.org/x/crypto/scrypt"
)

// VerifyPassword checks the password against the hash.
// It supports Argon2id (standard), scrypt (legacy), bcrypt (legacy), and pbkdf2 (legacy).
// It returns true if valid, and a bool indicating if the hash is legacy and should be upgraded.
func VerifyPassword(hashStr, password string) (bool, bool, error) {
	slog.Info("VerifyPassword called", "hash_len", len(hashStr))

	// 1. Check for Argon2id (Standard)
	if strings.HasPrefix(hashStr, "$argon2id") {
		valid, err := verifyArgon2id(hashStr, password)

		return valid, false, err // Valid and current
	}

	// 2. Check for Scrypt (Legacy)
	if strings.HasPrefix(hashStr, "scrypt:") {
		valid, err := verifyScrypt(hashStr, password)

		return valid, true, err // Valid but legacy
	}

	// 3. Check for Bcrypt (Legacy)
	if strings.HasPrefix(hashStr, "$2") {
		err := bcrypt.CompareHashAndPassword([]byte(hashStr), []byte(password))
		if err == nil {
			return true, true, nil // Valid but legacy
		}

		return false, false, err
	}

	if strings.HasPrefix(hashStr, "pbkdf2:sha256:") {
		valid, err := verifyPbkdf2(hashStr, password)

		return valid, true, err // Valid but legacy
	}

	return false, false, errors.New("unknown hash format")
}

// HashPassword generates an Argon2id hash of the password.
func HashPassword(password string) (string, error) {
	const (
		time    = 1
		memory  = 64 * 1024
		threads = 1
		keyLen  = 32
	)

	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	hash := argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)

	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(hash)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, memory, time, threads, b64Salt, b64Hash), nil
}

func verifyArgon2id(hashStr, password string) (bool, error) {
	parts := strings.Split(hashStr, "$")
	if len(parts) != 6 {
		return false, errors.New("invalid argon2id format")
	}

	// parts[0] is empty
	// parts[1] is "argon2id"
	// parts[2] is "v=19"
	// parts[3] is "m=65536,t=1,p=1"
	// parts[4] is salt (base64)
	// parts[5] is hash (base64)

	var version int
	_, err := fmt.Sscanf(parts[2], "v=%d", &version)
	if err != nil {
		return false, err
	}
	if version != argon2.Version {
		return false, errors.New("incompatible argon2 version")
	}

	var memory, time, threads uint32
	_, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads)
	if err != nil {
		return false, err
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}

	decodedHash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}

	keyLen := uint32(len(decodedHash))

	hash := argon2.IDKey([]byte(password), salt, time, memory, uint8(threads), keyLen)

	return subtle.ConstantTimeCompare(decodedHash, hash) == 1, nil
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
