package auth

import (
	"github.com/golang-jwt/jwt/v5"
	"testing"
)

func TestMCPRequiresResourceAudienceAndExactOrganizationRoles(t *testing.T) {
	s := newSigner(t)
	v := NewVerifier(testIssuer, "gateway-project", s.keyfunc).ForMCP(testAudience, "platform-org")
	valid := func() jwt.MapClaims {
		c := validClaims()
		c["aud"] = []string{"gateway-project", testAudience}
		c["client_id"] = "dedicated-agent"
		c["urn:zitadel:iam:org:project:gateway-project:roles"] = map[string]any{
			"agentgateway.mcp":            map[string]any{"platform-org": "tesserix.app"},
			"onboarding.helivanta.read":   map[string]any{"platform-org": "tesserix.app"},
			"onboarding.helivanta.create": map[string]any{"platform-org": "tesserix.app"},
		}
		return c
	}
	got, err := v.Verify(t.Context(), s.sign(t, jwt.SigningMethodRS256, valid()))
	if err != nil || !got.MCPRead || !got.MCPCreate || got.MCPAdopt || got.Subject != "svc-planning-poker" {
		t.Fatalf("unexpected MCP authority: %+v %v", got, err)
	}
	for name, mutate := range map[string]func(jwt.MapClaims){
		"gateway only":  func(c jwt.MapClaims) { c["aud"] = "gateway-project" },
		"resource only": func(c jwt.MapClaims) { c["aud"] = testAudience },
		"id token":      func(c jwt.MapClaims) { delete(c, "client_id") },
		"generic role projection": func(c jwt.MapClaims) {
			c["urn:zitadel:iam:org:project:roles"] = c["urn:zitadel:iam:org:project:gateway-project:roles"]
			delete(c, "urn:zitadel:iam:org:project:gateway-project:roles")
		},
		"foreign organization": func(c jwt.MapClaims) {
			c["urn:zitadel:iam:org:project:gateway-project:roles"] = map[string]any{"onboarding.helivanta.read": map[string]any{"other-org": "other.app"}}
		},
		"no onboarding scope": func(c jwt.MapClaims) {
			c["urn:zitadel:iam:org:project:gateway-project:roles"] = map[string]any{"agentgateway.mcp": map[string]any{"platform-org": "tesserix.app"}}
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := valid()
			mutate(c)
			if _, e := v.Verify(t.Context(), s.sign(t, jwt.SigningMethodRS256, c)); e == nil {
				t.Fatal("unexpected authorization")
			}
		})
	}
}
