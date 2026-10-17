package auth

import (
	"context"
	"github.com/golang-jwt/jwt/v5"
	"testing"
)

func TestConsoleAccessRequiresExactClientProjectAndPlatformGrant(t *testing.T) {
	s := newSigner(t)
	const project = "console-project"
	const org = "platform-org"
	const client = "console-client"
	rolesKey := "urn:zitadel:iam:org:project:" + project + ":roles"
	for _, tc := range []struct {
		name   string
		change func(jwt.MapClaims)
		want   string
	}{
		{"administrator", func(c jwt.MapClaims) {}, "platform-admin"},
		{"reader", func(c jwt.MapClaims) { c[rolesKey] = map[string]any{"platform": map[string]any{org: "example.test"}} }, "platform-operator"},
		{"other client", func(c jwt.MapClaims) { c["client_id"] = "product-client" }, ""},
		{"id token", func(c jwt.MapClaims) { delete(c, "client_id") }, ""},
		{"other audience", func(c jwt.MapClaims) { c["aud"] = "other-project" }, ""},
		{"other project roles", func(c jwt.MapClaims) { c["urn:zitadel:iam:org:project:other:roles"] = c[rolesKey]; delete(c, rolesKey) }, ""},
		{"foreign org", func(c jwt.MapClaims) {
			c[rolesKey] = map[string]any{"platform": map[string]any{"foreign": "foreign.test"}, "rotate-credentials": map[string]any{"foreign": "foreign.test"}}
		}, ""},
		{"missing platform", func(c jwt.MapClaims) {
			c[rolesKey] = map[string]any{"rotate-credentials": map[string]any{org: "example.test"}}
		}, ""},
		{"foreign verb", func(c jwt.MapClaims) {
			c[rolesKey] = map[string]any{"platform": map[string]any{org: "example.test"}, "rotate-credentials": map[string]any{"foreign": "foreign.test"}}
		}, "platform-operator"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := validClaims()
			c["aud"] = project
			c["client_id"] = client
			c[rolesKey] = map[string]any{"platform": map[string]any{org: "example.test"}, "rotate-credentials": map[string]any{org: "example.test"}}
			tc.change(c)
			got, err := NewVerifier(testIssuer, project, s.keyfunc).ForConsole(client, org).Verify(context.Background(), s.sign(t, jwt.SigningMethodRS256, c))
			if tc.want == "" {
				if err == nil {
					t.Fatal("unauthorized console token accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.OrgID != org || len(got.Roles) != 1 || got.Roles[0] != tc.want || !got.Console {
				t.Fatalf("wrong console authority: %+v", got)
			}
		})
	}
}
