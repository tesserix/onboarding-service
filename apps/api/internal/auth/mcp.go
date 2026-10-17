package auth

import (
	"github.com/golang-jwt/jwt/v5"
	"slices"
)

// ForMCP requires the onboarding resource audience as well as gateway admission.
func (v *Verifier) ForMCP(resourceAudience, organization string) *Verifier {
	copy := *v
	copy.mcpResource, copy.mcpOrg = resourceAudience, organization
	return &copy
}

func (v *Verifier) mcpClaims(mapped jwt.MapClaims, claims Claims) (Claims, error) {
	audiences, err := mapped.GetAudience()
	client, _ := mapped["client_id"].(string)
	if err != nil || !slices.Contains(audiences, v.mcpResource) || client == "" || v.mcpOrg == "" {
		return Claims{}, ErrInvalidToken
	}
	grants, _ := mapped["urn:zitadel:iam:org:project:"+v.audience+":roles"].(map[string]any)
	has := func(role string) bool {
		orgs, _ := grants[role].(map[string]any)
		domain, ok := orgs[v.mcpOrg].(string)
		return ok && domain != ""
	}
	claims.MCPRead = has("onboarding.helivanta.read")
	claims.MCPCreate = has("onboarding.helivanta.create")
	claims.MCPAdopt = has("onboarding.helivanta.adopt")
	if !has("agentgateway.onboarding") || !claims.MCPRead {
		return Claims{}, ErrInvalidToken
	}
	return claims, nil
}
