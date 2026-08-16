// Package auth verifies the two kinds of caller this API has: a product's
// machine user, which presents a Zitadel JWT, and a platform or org-admin
// human, which presents a session cookie.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("invalid token")

type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Nonce         string
	OrgID         string
	Roles         []string
	ExpiresAt     time.Time
}

type Verifier struct {
	issuer   string
	audience string
	keyfunc  jwt.Keyfunc
	parser   *jwt.Parser
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
	if email, ok := mapped["email"].(string); ok {
		claims.Email = email
	}
	if verified, ok := mapped["email_verified"].(bool); ok {
		claims.EmailVerified = verified
	}
	if nonce, ok := mapped["nonce"].(string); ok {
		claims.Nonce = nonce
	}
	// Zitadel namespaces its own claims; the org is the one the user acted in.
	if org, ok := mapped["urn:zitadel:iam:user:resourceowner:id"].(string); ok {
		claims.OrgID = org
	}
	claims.Roles = projectRoles(mapped)
	return claims, nil
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
