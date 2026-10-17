// Package auth verifies the two kinds of caller this API has: a product's
// machine user, which presents a Zitadel JWT, and a platform or org-admin
// human, which presents a session cookie.
package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("invalid token")

type Claims struct {
	Console       bool
	HardDelete    bool
	Subject       string
	Email         string
	EmailVerified bool
	Nonce         string
	OrgID         string
	Roles         []string
	ExpiresAt     time.Time
}

type Verifier struct {
	consoleClient string
	consoleOrg    string
	issuer        string
	audience      string
	keyfunc       jwt.Keyfunc
	parser        *jwt.Parser
}

// ForConsole restricts a separate verifier to the console access-token contract.
func (v *Verifier) ForConsole(clientID, orgID string) *Verifier {
	copy := *v
	copy.consoleClient, copy.consoleOrg = clientID, orgID
	return &copy
}

// NewVerifier pins RS256: the algorithm comes from configuration, never from
// the token header, or alg:none and HMAC-with-the-public-key both verify.
func NewVerifier(issuer, audience string, kf jwt.Keyfunc) *Verifier {
	return &Verifier{
		issuer:   issuer,
		audience: audience,
		keyfunc:  kf,
		parser: jwt.NewParser(
			jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
			jwt.WithIssuer(issuer),
			jwt.WithAudience(audience),
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
			jwt.WithLeeway(30*time.Second),
		),
	}
}

// NewJWKSVerifier resolves signing keys from the issuer's JWKS, refreshed in
// the background so a key rotation does not need a restart.
func NewJWKSVerifier(ctx context.Context, issuer, jwksURL, audience string, client *http.Client) (*Verifier, error) {
	kf, err := keyfunc.NewDefaultOverrideCtx(ctx, []string{jwksURL}, keyfunc.Override{Client: client})
	if err != nil {
		return nil, fmt.Errorf("load jwks %s: %w", jwksURL, err)
	}
	return NewVerifier(issuer, audience, kf.Keyfunc), nil
}

func (v *Verifier) Verify(ctx context.Context, raw string) (Claims, error) {
	var mapped jwt.MapClaims
	token, err := v.parser.ParseWithClaims(raw, &mapped, v.keyfunc)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !token.Valid {
		return Claims{}, ErrInvalidToken
	}

	subject, _ := mapped.GetSubject()
	if subject == "" {
		return Claims{}, fmt.Errorf("%w: no subject", ErrInvalidToken)
	}
	expiry, err := mapped.GetExpirationTime()
	if err != nil || expiry == nil {
		return Claims{}, fmt.Errorf("%w: no expiry", ErrInvalidToken)
	}

	claims := Claims{Subject: subject, ExpiresAt: expiry.Time}
	if v.consoleClient != "" || v.consoleOrg != "" {
		client, _ := mapped["client_id"].(string)
		grants, _ := mapped["urn:zitadel:iam:org:project:"+v.audience+":roles"].(map[string]any)
		has := func(role string) bool {
			orgs, _ := grants[role].(map[string]any)
			domain, ok := orgs[v.consoleOrg].(string)
			return ok && domain != "" && v.consoleOrg != ""
		}
		if client != v.consoleClient || v.consoleClient == "" || !has("platform") {
			return Claims{}, ErrInvalidToken
		}
		claims.Console, claims.HardDelete = true, has("hard-delete")
		claims.OrgID = v.consoleOrg
		claims.Roles = []string{"platform-operator"}
		if has("rotate-credentials") {
			claims.Roles = []string{"platform-admin"}
		}
		return claims, nil
	}
	if email, ok := mapped["email"].(string); ok {
		claims.Email = email
	}
	if verified, ok := mapped["email_verified"].(bool); ok {
		claims.EmailVerified = verified
	}
	if nonce, ok := mapped["nonce"].(string); ok {
		claims.Nonce = nonce
	}
	// The explicit organization scope is the org the user is acting in. The
	// resource owner is only a fallback; for a platform-created human it remains
	// TESSERIX even after that human switches into a customer organization.
	if org, ok := mapped["urn:zitadel:iam:org:id"].(string); ok && org != "" {
		claims.OrgID = org
	} else if org := registryTenantMetadata(
		mapped["urn:zitadel:iam:user:metadata"], mapped["urn:zitadel:iam:org:project:roles"],
	); org != "" {
		claims.OrgID = org
	} else if org := registryRoleOrganization(mapped["urn:zitadel:iam:org:project:roles"]); org != "" {
		claims.OrgID = org
	} else if org, ok := mapped["urn:zitadel:iam:user:resourceowner:id"].(string); ok {
		claims.OrgID = org
	}
	claims.Roles = projectRoles(mapped)
	return claims, nil
}

func registryRoleOrganization(value any) string {
	organizations := registryRoleOrganizations(value)
	if len(organizations) != 1 {
		return ""
	}
	for orgID := range organizations {
		return orgID
	}
	return ""
}

func registryRoleOrganizations(value any) map[string]bool {
	organizations := map[string]bool{}
	roles, ok := value.(map[string]any)
	if !ok {
		return organizations
	}
	for _, role := range []string{"registry.reader", "registry.publisher", "registry.deleter"} {
		grants, ok := roles[role].(map[string]any)
		if !ok {
			continue
		}
		for orgID := range grants {
			if orgID != "" {
				organizations[orgID] = true
			}
		}
	}
	return organizations
}

func registryTenantMetadata(metadataValue, roleValue any) string {
	metadata, ok := metadataValue.(map[string]any)
	if !ok {
		return ""
	}
	encoded, ok := metadata["registry_tenant"].(string)
	if !ok || encoded == "" {
		return ""
	}
	var raw []byte
	var err error
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding,
	} {
		raw, err = encoding.DecodeString(encoded)
		if err == nil {
			break
		}
	}
	if err != nil {
		return ""
	}
	candidate := strings.TrimSpace(string(raw))
	if candidate == "" || len(candidate) > 200 || !registryRoleOrganizations(roleValue)[candidate] {
		return ""
	}
	return candidate
}

// Zitadel projects roles as {"role": {"orgId": "domain"}}; only the key matters
// here, org scoping is decided from the projection, not from the token's map.
func projectRoles(claims jwt.MapClaims) []string {
	grants, ok := claims["urn:zitadel:iam:org:project:roles"].(map[string]any)
	if !ok {
		return nil
	}
	roles := make([]string, 0, len(grants))
	for role := range grants {
		roles = append(roles, role)
	}
	return roles
}
