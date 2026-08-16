// Package config reads the environment once, at startup, and refuses to start
// on a missing secret rather than failing on the first request that needs it.
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Port              string
	DatabaseURL       string
	ZitadelIssuer     string
	ZitadelJWKSURL    string
	ZitadelPAT        string
	ZitadelMachineKey string
	// PlatformOrgID is optional: an empty value is resolved from
	// PlatformOrgName at boot.
	PlatformOrgID   string
	PlatformOrgName string
	Audience        string
	ConsoleClientID string
	ConsoleURL      string
	RedirectURI     string
	SessionKey      []byte
	SecureCookies   bool
	ShutdownGrace   time.Duration
}

func Load() (Config, error) {
	c := Config{
		Port:              env("PORT", "8080"),
		DatabaseURL:       os.Getenv("DATABASE_URL"),
		ZitadelIssuer:     env("ZITADEL_ISSUER", "https://auth.tesserix.app"),
		ZitadelPAT:        os.Getenv("ZITADEL_PAT"),
		ZitadelMachineKey: os.Getenv("ZITADEL_MACHINE_KEY"),
		PlatformOrgID:     os.Getenv("PLATFORM_ORG_ID"),
		PlatformOrgName:   env("PLATFORM_ORG_NAME", "TESSERIX"),
		Audience:          env("TOKEN_AUDIENCE", "onboarding-api"),
		ConsoleClientID:   os.Getenv("CONSOLE_CLIENT_ID"),
		ConsoleURL:        env("CONSOLE_URL", "https://onboard.tesserix.app"),
		SecureCookies:     env("SECURE_COOKIES", "true") == "true",
		ShutdownGrace:     20 * time.Second,
	}
	c.ZitadelJWKSURL = env("ZITADEL_JWKS_URL", strings.TrimSuffix(c.ZitadelIssuer, "/")+"/oauth/v2/keys")
	c.RedirectURI = env("OIDC_REDIRECT_URI", strings.TrimSuffix(c.ConsoleURL, "/")+"/api/auth/callback")

	key, err := sessionKey(os.Getenv("SESSION_KEY"))
	if err != nil {
		return Config{}, err
	}
	c.SessionKey = key

	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	// Two credentials configured is worse than none: which one is in use stops
	// being knowable from the manifest.
	if (c.ZitadelPAT == "") == (c.ZitadelMachineKey == "") {
		return Config{}, fmt.Errorf("set exactly one of ZITADEL_MACHINE_KEY or ZITADEL_PAT")
	}
	if c.PlatformOrgName == "" {
		return Config{}, fmt.Errorf("PLATFORM_ORG_NAME is required")
	}
	return c, nil
}

// The session key seals every console cookie, so a short or missing one is a
// startup failure, not a warning.
func sessionKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, fmt.Errorf("SESSION_KEY is required")
	}
	// A 32-character raw key is also valid base64 that decodes to 24 bytes, so
	// the literal length is checked before the base64 reading.
	if len(raw) == 32 {
		return []byte(raw), nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("SESSION_KEY must be 32 bytes or base64 for 32 bytes")
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("SESSION_KEY must decode to 32 bytes, got %d", len(key))
	}
	return key, nil
}

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func (c Config) PoolSize() int {
	if v, err := strconv.Atoi(os.Getenv("DB_MAX_CONNS")); err == nil && v > 0 {
		return v
	}
	return 10
}
