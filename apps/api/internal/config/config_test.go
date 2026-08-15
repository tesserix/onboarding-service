package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_URL", "postgres://localhost/onboarding_db")
	t.Setenv("ZITADEL_PAT", "pat-token")
	t.Setenv("PLATFORM_ORG_ID", "999")
	t.Setenv("SESSION_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
}

func TestLoadDerivesJWKSAndRedirectFromDefaults(t *testing.T) {
	setRequired(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ZitadelJWKSURL != "https://auth.tesserix.app/oauth/v2/keys" {
		t.Errorf("jwks url = %q", cfg.ZitadelJWKSURL)
	}
	if cfg.RedirectURI != "https://onboard.tesserix.app/api/auth/callback" {
		t.Errorf("redirect uri = %q", cfg.RedirectURI)
	}
	if cfg.Port != "8080" || !cfg.SecureCookies {
		t.Errorf("port = %q, secureCookies = %v", cfg.Port, cfg.SecureCookies)
	}
	if len(cfg.SessionKey) != 32 {
		t.Errorf("session key length = %d", len(cfg.SessionKey))
	}
}

func TestLoadDerivedURLsDoNotDoubleSlash(t *testing.T) {
	setRequired(t)
	t.Setenv("ZITADEL_ISSUER", "https://auth.example.test/")
	t.Setenv("CONSOLE_URL", "https://onboard.example.test/")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ZitadelJWKSURL != "https://auth.example.test/oauth/v2/keys" {
		t.Errorf("jwks url = %q", cfg.ZitadelJWKSURL)
	}
	if cfg.RedirectURI != "https://onboard.example.test/api/auth/callback" {
		t.Errorf("redirect uri = %q", cfg.RedirectURI)
	}
}

func TestLoadRejectsMissingSecrets(t *testing.T) {
	for _, name := range []string{"DATABASE_URL", "ZITADEL_PAT", "PLATFORM_ORG_ID", "SESSION_KEY"} {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(name, "")

			_, err := Load()
			if err == nil {
				t.Fatalf("Load succeeded without %s", name)
			}
			if !strings.Contains(err.Error(), name) {
				t.Errorf("error %q does not name %s", err, name)
			}
		})
	}
}

func TestSessionKeyLength(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{"base64 32 bytes", base64.StdEncoding.EncodeToString(make([]byte, 32)), false},
		{"raw 32 bytes", strings.Repeat("k", 32), false},
		{"base64 16 bytes", base64.StdEncoding.EncodeToString(make([]byte, 16)), true},
		{"raw too short", "short", true},
		{"empty", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, err := sessionKey(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("sessionKey(%q) accepted a %d byte key", tc.raw, len(key))
				}
				return
			}
			if err != nil {
				t.Fatalf("sessionKey: %v", err)
			}
			if len(key) != 32 {
				t.Errorf("key length = %d", len(key))
			}
		})
	}
}

func TestPoolSize(t *testing.T) {
	var cfg Config
	for _, tc := range []struct{ raw, name string }{{"", "unset"}, {"0", "zero"}, {"-4", "negative"}, {"nope", "not a number"}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DB_MAX_CONNS", tc.raw)
			if got := cfg.PoolSize(); got != 10 {
				t.Errorf("PoolSize() = %d, want the default 10", got)
			}
		})
	}
	t.Setenv("DB_MAX_CONNS", "25")
	if got := cfg.PoolSize(); got != 25 {
		t.Errorf("PoolSize() = %d, want 25", got)
	}
}
