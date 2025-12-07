package config

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/caarlos0/env/v6"
	"github.com/joho/godotenv"
)

type Settings struct {
	SecretKey              string `env:"SECRET_KEY"`
	DBDSN                  string `env:"DB_DSN" envDefault:"tronbyt.db"`
	DataDir                string `env:"DATA_DIR" envDefault:"data"`
	Production             string `env:"PRODUCTION" envDefault:"0"`
	EnableUserRegistration string `env:"ENABLE_USER_REGISTRATION" envDefault:"1"`
	MaxUsers               int    `env:"MAX_USERS" envDefault:"0"`
	SingleUserAutoLogin    string `env:"SINGLE_USER_AUTO_LOGIN" envDefault:"0"`
	SystemAppsRepo         string `env:"SYSTEM_APPS_REPO" envDefault:"https://github.com/tronbyt/apps.git"`
	RedisURL               string `env:"REDIS_URL"`
	Host                   string `env:"TRONBYT_HOST" envDefault:""`
	Port                   string `env:"TRONBYT_PORT" envDefault:"8000"`
	UnixSocket             string `env:"TRONBYT_UNIX_SOCKET"`
	SSLKeyFile             string `env:"TRONBYT_SSL_KEYFILE"`
	SSLCertFile            string `env:"TRONBYT_SSL_CERTFILE"`
	TrustedProxies         string `env:"TRONBYT_TRUSTED_PROXIES" envDefault:"*"`
}

// TemplateConfig holds configuration values needed in templates
type TemplateConfig struct {
	EnableUserRegistration string
	SingleUserAutoLogin    string
	Production             string
}

func LoadSettings() (*Settings, error) {
	// Load .env file if it exists
	if _, err := os.Stat(".env"); err == nil {
		if err := godotenv.Load(); err != nil {
			slog.Warn("Error loading .env file", "error", err)
		}
	}

	cfg := Settings{}
	if err := env.Parse(&cfg); err != nil {
		return nil, err
	}

	if cfg.SecretKey == "" {
		secretKey, err := loadOrGenerateSecret(cfg.DataDir)
		if err != nil {
			return nil, fmt.Errorf("failed to load or generate secret key: %w", err)
		}
		cfg.SecretKey = secretKey
	}

	return &cfg, nil
}

func loadOrGenerateSecret(dataDir string) (string, error) {
	keyFile := filepath.Join(dataDir, ".secret_key")
	// Ensure data directory exists
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			slog.Error("Failed to create data directory", "path", dataDir, "error", err)
			return "", err
		}
	}

	if content, err := os.ReadFile(keyFile); err == nil {
		return string(content), nil
	}

	slog.Warn("SECRET_KEY not set, generating random key and saving to file", "path", keyFile)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		slog.Error("Failed to generate random secret key", "error", err)
		return "", err // Return error here
	}
	secret := base64.StdEncoding.EncodeToString(key)

	if err := os.WriteFile(keyFile, []byte(secret), 0600); err != nil {
		slog.Error("Failed to write secret key file", "error", err)
		return "", err // Return error here
	}
	return secret, nil
}
