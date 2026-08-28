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
	t.Setenv("GOOGLE_CLIENT_ID", "client-1.apps.googleusercontent.com")
	t.Setenv("GOOGLE_CLIENT_SECRET", "client-secret")
	t.Setenv("PLATFORM_ADMIN_EMAILS", "first@example.test")
	t.Setenv("REGISTRY_TOKEN_AUDIENCE", "386930054896026901")
	t.Setenv("REGISTRY_PROJECT_ID", "386930054896026901")
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
	// PLATFORM_ORG_ID is deliberately absent: it is resolved from the org name
	// at boot, so requiring it here would re-introduce the hand-copied value.
	for _, name := range []string{"DATABASE_URL", "ZITADEL_PAT", "SESSION_KEY"} {
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

func TestLoadRequiresRegistryIdentitySettings(t *testing.T) {
	for _, name := range []string{"REGISTRY_TOKEN_AUDIENCE", "REGISTRY_PROJECT_ID"} {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(name, "")
			if _, err := Load(); err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("Load without %s: %v", name, err)
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

// The machine key is the credential this service is meant to use: it is
// short-lived, scoped, and revocable by deleting the key in Zitadel. The PAT is
// instance-wide and cannot be any of those, so it is only accepted alone.
func TestLoadRequiresExactlyOneZitadelCredential(t *testing.T) {
	for name, setup := range map[string]func(*testing.T){
		"neither": func(t *testing.T) {
			t.Setenv("ZITADEL_PAT", "")
			t.Setenv("ZITADEL_MACHINE_KEY", "")
		},
		"both": func(t *testing.T) {
			t.Setenv("ZITADEL_PAT", "pat-token")
			t.Setenv("ZITADEL_MACHINE_KEY", `{"keyId":"k"}`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			setRequired(t)
			setup(t)
			if _, err := Load(); err == nil {
				t.Fatal("a service with no single Zitadel credential started")
			}
		})
	}

	t.Run("machine key alone", func(t *testing.T) {
		setRequired(t)
		t.Setenv("ZITADEL_PAT", "")
		t.Setenv("ZITADEL_MACHINE_KEY", `{"keyId":"k"}`)
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.ZitadelMachineKey != `{"keyId":"k"}` {
			t.Fatalf("machine key not read: %q", cfg.ZitadelMachineKey)
		}
	})
}

// A hand-copied org id is stale the moment the org is recreated, so the name is
// what is configured and the id is resolved from it at boot.
func TestPlatformOrgIsNamedNotNumbered(t *testing.T) {
	setRequired(t)
	t.Setenv("PLATFORM_ORG_ID", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("an empty PLATFORM_ORG_ID must be resolvable from the name: %v", err)
	}
	if cfg.PlatformOrgName != "TESSERIX" {
		t.Fatalf("platform org name = %q, want TESSERIX", cfg.PlatformOrgName)
	}
}

func TestLoadRequiresTheConsoleSignInSettings(t *testing.T) {
	for _, name := range []string{"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "PLATFORM_ADMIN_EMAILS"} {
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

// An allowlist of only blanks parses to nothing, which would leave the console
// with no admins at all — that is a misconfiguration, not an empty list.
func TestLoadRejectsAnAllowlistThatNamesNobody(t *testing.T) {
	setRequired(t)
	t.Setenv("PLATFORM_ADMIN_EMAILS", " , ,")

	if _, err := Load(); err == nil {
		t.Fatal("Load accepted an allowlist with no addresses on it")
	}
}

func TestLoadKeepsTheAllowlistVerbatim(t *testing.T) {
	setRequired(t)
	t.Setenv("PLATFORM_ADMIN_EMAILS", "one@example.test, two@example.test")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PlatformAdminEmails != "one@example.test, two@example.test" {
		t.Fatalf("allowlist = %q", cfg.PlatformAdminEmails)
	}
}
