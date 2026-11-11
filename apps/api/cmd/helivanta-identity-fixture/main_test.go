//go:build identityfixture

package main

import "testing"

func TestFixtureConfigurationCannotTargetProduction(t *testing.T) {
	const local = "postgres://helivanta:helivanta@127.0.0.1:5432/onboarding_fixture?sslmode=disable"
	for _, tc := range []struct {
		name, enabled, issuer, dsn string
		allowed                    bool
	}{
		{"local explicit", "true", localIssuer, local, true},
		{"not enabled", "", localIssuer, local, false},
		{"production issuer", "true", "https://auth.tesserix.app", local, false},
		{"remote database", "true", localIssuer, "postgres://helivanta:helivanta@db.example:5432/onboarding_fixture", false},
		{"business database", "true", localIssuer, "postgres://helivanta:helivanta@127.0.0.1:5432/helivanta", false},
		{"dsn options", "true", localIssuer, local + "&options=-csearch_path=public", false},
		{"other credentials", "true", localIssuer, "postgres://root:password@127.0.0.1:5432/onboarding_fixture?sslmode=disable", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if (validateConfig(tc.enabled, tc.issuer, tc.dsn) == nil) != tc.allowed {
				t.Fatal("unsafe fixture configuration")
			}
		})
	}
}
