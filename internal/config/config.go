package config

import (
	"log/slog"
	"os"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type Settings struct {
	DBDSN                  string `env:"DB_DSN"                   envDefault:"data/tronbyt.db"`
	DataDir                string `env:"DATA_DIR"                 envDefault:"data"`
	Production             bool   `env:"PRODUCTION"               envDefault:"true"`
	EnableUserRegistration bool   `env:"ENABLE_USER_REGISTRATION" envDefault:"true"`
	EnablePprof            bool   `env:"ENABLE_PPROF"`
	MaxUsers               int    `env:"MAX_USERS"`
	SingleUserAutoLogin    bool   `env:"SINGLE_USER_AUTO_LOGIN"`
	SystemAppsAutoRefresh  bool   `env:"SYSTEM_APPS_AUTO_REFRESH"`
	SystemAppsRepo         string `env:"SYSTEM_APPS_REPO"         envDefault:"https://github.com/tronbyt/apps.git"`
	GitHubToken            string `env:"GITHUB_TOKEN"`
	RedisURL               string `env:"REDIS_URL"`
	Host                   string `env:"TRONBYT_HOST"             envDefault:""`
	Port                   string `env:"TRONBYT_PORT"             envDefault:"8000"`
	UnixSocket             string `env:"TRONBYT_UNIX_SOCKET"`
	SSLKeyFile             string `env:"TRONBYT_SSL_KEYFILE"`
	SSLCertFile            string `env:"TRONBYT_SSL_CERTFILE"`
	TrustedProxies         string `env:"TRONBYT_TRUSTED_PROXIES"  envDefault:"*"`
	LogLevel               string `env:"LOG_LEVEL"                envDefault:"INFO"`
	EnableUpdateChecks     bool   `env:"ENABLE_UPDATE_CHECKS"     envDefault:"true"`
}

// TemplateConfig holds configuration values needed in templates.
type TemplateConfig struct {
	EnableUserRegistration bool
	SingleUserAutoLogin    bool
	SystemAppsAutoRefresh  bool
	Production             bool
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

	return &cfg, nil
}
