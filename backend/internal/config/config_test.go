package config

import "testing"

func TestLoadAuthPolicyFromEnvironment(t *testing.T) {
	t.Setenv("LGFM_DATA_DIR", t.TempDir())
	t.Setenv("LGFM_LOGIN_RATE_LIMIT_MAX", "17")
	t.Setenv("LGFM_LOGIN_RATE_LIMIT_SEC", "90")
	t.Setenv("LGFM_SESSION_TTL_HOURS", "24")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LoginRateLimitMax != 17 || cfg.LoginRateLimitSec != 90 || cfg.SessionTTLHours != 24 {
		t.Fatalf("unexpected auth policy: %+v", cfg)
	}
}

func TestLoadAuthPolicyRejectsInvalidValues(t *testing.T) {
	t.Setenv("LGFM_DATA_DIR", t.TempDir())
	t.Setenv("LGFM_LOGIN_RATE_LIMIT_MAX", "0")
	t.Setenv("LGFM_LOGIN_RATE_LIMIT_SEC", "invalid")
	t.Setenv("LGFM_SESSION_TTL_HOURS", "-1")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LoginRateLimitMax != 10 || cfg.LoginRateLimitSec != 60 || cfg.SessionTTLHours != 12 {
		t.Fatalf("invalid values did not fall back: %+v", cfg)
	}
}
