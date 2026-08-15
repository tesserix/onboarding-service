package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://auth.tesserix.app"
	testAudience = "onboarding-api"
)

type signer struct {
	key *rsa.PrivateKey
}

func newSigner(t *testing.T) *signer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return &signer{key: key}
}

func (s *signer) keyfunc(*jwt.Token) (any, error) { return &s.key.PublicKey, nil }

func (s *signer) sign(t *testing.T, method jwt.SigningMethod, claims jwt.MapClaims) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	var (
		raw string
		err error
	)
	switch method {
	case jwt.SigningMethodNone:
		raw, err = tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	case jwt.SigningMethodHS256:
		// Algorithm confusion: sign with the *public* key as an HMAC secret.
		raw, err = tok.SignedString([]byte("public-key-as-hmac-secret"))
	default:
		raw, err = tok.SignedString(s.key)
	}
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func validClaims() jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss":   testIssuer,
		"aud":   testAudience,
		"sub":   "svc-planning-poker",
		"exp":   now.Add(time.Hour).Unix(),
		"iat":   now.Unix(),
		"nbf":   now.Unix(),
		"email": "svc@tesserix.app",
	}
}

func newVerifier(s *signer) *Verifier {
	return NewVerifier(testIssuer, testAudience, s.keyfunc)
}

func TestAValidTokenYieldsItsSubject(t *testing.T) {
	s := newSigner(t)
	raw := s.sign(t, jwt.SigningMethodRS256, validClaims())

	claims, err := newVerifier(s).Verify(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Subject != "svc-planning-poker" {
		t.Fatalf("subject %q", claims.Subject)
	}
}

// alg:none and HMAC-with-the-public-key are the two classic forgeries. The
// verifier must pin RS256 rather than trusting the header.
func TestUnexpectedSigningAlgorithmsAreRejected(t *testing.T) {
	for _, method := range []jwt.SigningMethod{jwt.SigningMethodNone, jwt.SigningMethodHS256} {
		t.Run(method.Alg(), func(t *testing.T) {
			s := newSigner(t)
			raw := s.sign(t, method, validClaims())
			if _, err := newVerifier(s).Verify(context.Background(), raw); err == nil {
				t.Fatalf("%s token accepted", method.Alg())
			}
		})
	}
}

func TestClaimsAreCheckedNotJustTheSignature(t *testing.T) {
	cases := map[string]func(jwt.MapClaims){
		"wrong issuer":   func(c jwt.MapClaims) { c["iss"] = "https://evil.example" },
		"wrong audience": func(c jwt.MapClaims) { c["aud"] = "some-other-api" },
		"expired":        func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() },
		"not yet valid":  func(c jwt.MapClaims) { c["nbf"] = time.Now().Add(time.Hour).Unix() },
		"no subject":     func(c jwt.MapClaims) { delete(c, "sub") },
		"no expiry":      func(c jwt.MapClaims) { delete(c, "exp") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s := newSigner(t)
			claims := validClaims()
			mutate(claims)
			if _, err := newVerifier(s).Verify(context.Background(), s.sign(t, jwt.SigningMethodRS256, claims)); err == nil {
				t.Fatalf("accepted a token with %s", name)
			}
		})
	}
}

func TestATokenSignedByAnotherKeyIsRejected(t *testing.T) {
	issued := newSigner(t)
	raw := issued.sign(t, jwt.SigningMethodRS256, validClaims())

	if _, err := newVerifier(newSigner(t)).Verify(context.Background(), raw); err == nil {
		t.Fatal("token signed by an unknown key accepted")
	}
}

func TestGarbageIsRejectedWithoutPanicking(t *testing.T) {
	s := newSigner(t)
	for _, raw := range []string{"", "not.a.token", "a.b", "...."} {
		if _, err := newVerifier(s).Verify(context.Background(), raw); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
