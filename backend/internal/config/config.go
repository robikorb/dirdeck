package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Environment variable prefixes. LegacyEnvPrefix predates the DirDeck rename and
// stays supported so existing installations upgrade without editing .env.
const (
	EnvPrefix       = "DIRDECK_"
	LegacyEnvPrefix = "LGFM_"
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
	WritableVolumes   []string
	LoginRateLimitMax int
	LoginRateLimitSec int
	SessionTTLHours   int
}

// Load reads configuration from environment variables with sensible defaults.
//
// Every setting is read as DIRDECK_<NAME> first and falls back to the legacy
// LGFM_<NAME> from before the rename. Existing installations keep working
// untouched; a deprecation notice is logged once per legacy name so operators
// can migrate at their own pace.
func Load() (Config, error) {
	cfg := Config{
		ListenAddr:        envOr("LISTEN_ADDR", ":8080"),
		DataDir:           envOr("DATA_DIR", "./data"),
		VolumesFile:       envOr("VOLUMES_FILE", "./config/volumes.yaml"),
		AdminUsernameFile: envOr("ADMIN_USERNAME_FILE", "./secrets/admin_username"),
		AdminPasswordFile: envOr("ADMIN_PASSWORD_FILE", "./secrets/admin_password"),
		SecureCookie:      envOr("SECURE_COOKIE", "false") == "true",
		StaticDir:         envOr("STATIC_DIR", ""),
		WritableVolumes:   splitList(envOr("WRITABLE", "")),
		LoginRateLimitMax: envIntOr("LOGIN_RATE_LIMIT_MAX", 10),
		LoginRateLimitSec: envIntOr("LOGIN_RATE_LIMIT_SEC", 60),
		SessionTTLHours:   envIntOr("SESSION_TTL_HOURS", 12),
	}
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return Config{}, fmt.Errorf("create data dir: %w", err)
	}
	return cfg, nil
}

// lookupEnv resolves a setting from the current prefix, then the legacy one.
func lookupEnv(name string) (string, bool) {
	if v := strings.TrimSpace(os.Getenv(EnvPrefix + name)); v != "" {
		return v, true
	}
	legacy := LegacyEnvPrefix + name
	if v := strings.TrimSpace(os.Getenv(legacy)); v != "" {
		log.Printf("config: %s is deprecated, rename it to %s", legacy, EnvPrefix+name)
		return v, true
	}
	return "", false
}

// splitList parses a comma-separated setting such as DIRDECK_WRITABLE=media,docs.
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func envIntOr(name string, fallback int) int {
	raw, ok := lookupEnv(name)
	if !ok {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envOr(name, fallback string) string {
	if v, ok := lookupEnv(name); ok {
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
