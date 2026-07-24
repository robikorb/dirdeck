package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds process configuration loaded from the environment.
type Config struct {
	ListenAddr        string
	DataDir           string
	VolumesFile       string
	AdminUsernameFile string
	AdminPasswordFile string
	SecureCookie      bool
	StaticDir         string
	LoginRateLimitMax int
	LoginRateLimitSec int
	SessionTTLHours   int
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (Config, error) {
	cfg := Config{
		ListenAddr:        envOr("LGFM_LISTEN_ADDR", ":8080"),
		DataDir:           envOr("LGFM_DATA_DIR", "./data"),
		VolumesFile:       envOr("LGFM_VOLUMES_FILE", "./config/volumes.yaml"),
		AdminUsernameFile: envOr("LGFM_ADMIN_USERNAME_FILE", "./secrets/admin_username"),
		AdminPasswordFile: envOr("LGFM_ADMIN_PASSWORD_FILE", "./secrets/admin_password"),
		SecureCookie:      envOr("LGFM_SECURE_COOKIE", "false") == "true",
		StaticDir:         envOr("LGFM_STATIC_DIR", ""),
		LoginRateLimitMax: envIntOr("LGFM_LOGIN_RATE_LIMIT_MAX", 10),
		LoginRateLimitSec: envIntOr("LGFM_LOGIN_RATE_LIMIT_SEC", 60),
		SessionTTLHours:   envIntOr("LGFM_SESSION_TTL_HOURS", 12),
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return Config{}, fmt.Errorf("create data dir: %w", err)
	}
	return cfg, nil
}

func envIntOr(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// ReadSecretFile reads and trims a secret file.
func ReadSecretFile(path string) (string, error) {
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("read secret %s: %w", path, err)
	}
	v := strings.TrimSpace(string(b))
	if v == "" {
		return "", fmt.Errorf("secret file %s is empty", path)
	}
	return v, nil
}
