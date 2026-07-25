package config

import "testing"

func TestLoadAuthPolicyFromEnvironment(t *testing.T) {
	t.Setenv("DIRDECK_DATA_DIR", t.TempDir())
	t.Setenv("DIRDECK_LOGIN_RATE_LIMIT_MAX", "17")
	t.Setenv("DIRDECK_LOGIN_RATE_LIMIT_SEC", "90")
	t.Setenv("DIRDECK_SESSION_TTL_HOURS", "24")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LoginRateLimitMax != 17 || cfg.LoginRateLimitSec != 90 || cfg.SessionTTLHours != 24 {
		t.Fatalf("unexpected auth policy: %+v", cfg)
	}
}

func TestLoadAuthPolicyRejectsInvalidValues(t *testing.T) {
	t.Setenv("DIRDECK_DATA_DIR", t.TempDir())
	t.Setenv("DIRDECK_LOGIN_RATE_LIMIT_MAX", "0")
	t.Setenv("DIRDECK_LOGIN_RATE_LIMIT_SEC", "invalid")
	t.Setenv("DIRDECK_SESSION_TTL_HOURS", "-1")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LoginRateLimitMax != 10 || cfg.LoginRateLimitSec != 60 || cfg.SessionTTLHours != 12 {
		t.Fatalf("invalid values did not fall back: %+v", cfg)
	}
}

// Installations created before the DirDeck rename keep their LGFM_* .env files.
// Upgrading must not require editing them.
func TestLoadAcceptsLegacyEnvironmentPrefix(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LGFM_DATA_DIR", dir)
	t.Setenv("LGFM_LISTEN_ADDR", ":9999")
	t.Setenv("LGFM_SECURE_COOKIE", "true")
	t.Setenv("LGFM_LOGIN_RATE_LIMIT_MAX", "17")
	t.Setenv("LGFM_SESSION_TTL_HOURS", "24")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DataDir != dir {
		t.Fatalf("legacy data dir ignored: %q", cfg.DataDir)
	}
	if cfg.ListenAddr != ":9999" || !cfg.SecureCookie {
		t.Fatalf("legacy string/bool settings ignored: %+v", cfg)
	}
	if cfg.LoginRateLimitMax != 17 || cfg.SessionTTLHours != 24 {
		t.Fatalf("legacy int settings ignored: %+v", cfg)
	}
}

func TestCurrentEnvironmentPrefixWinsOverLegacy(t *testing.T) {
	t.Setenv("DIRDECK_DATA_DIR", t.TempDir())
	t.Setenv("LGFM_LISTEN_ADDR", ":1111")
	t.Setenv("DIRDECK_LISTEN_ADDR", ":2222")
	t.Setenv("LGFM_SESSION_TTL_HOURS", "5")
	t.Setenv("DIRDECK_SESSION_TTL_HOURS", "9")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddr != ":2222" {
		t.Fatalf("legacy string overrode current prefix: %q", cfg.ListenAddr)
	}
	if cfg.SessionTTLHours != 9 {
		t.Fatalf("legacy int overrode current prefix: %d", cfg.SessionTTLHours)
	}
}
