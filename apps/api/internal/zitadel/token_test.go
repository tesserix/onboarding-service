package zitadel

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func machineKey(t *testing.T, userID string) ([]byte, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	encoded := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	raw, err := json.Marshal(map[string]string{
		"type": "serviceaccount", "keyId": "key-1", "userId": userID, "key": string(encoded),
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw, &key.PublicKey
}

// tokenEndpoint stands in for Zitadel's /oauth/v2/token, and verifies the
// assertion the way Zitadel would before it hands anything back.
func tokenEndpoint(t *testing.T, pub *rsa.PublicKey, issuer string, expiresIn int, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if got := r.PostForm.Get("grant_type"); got != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("wrong grant type: %q", got)
		}
		claims := jwt.RegisteredClaims{}
		_, err := jwt.ParseWithClaims(r.PostForm.Get("assertion"), &claims,
			func(*jwt.Token) (any, error) { return pub, nil },
			jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(issuer))
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		if claims.Issuer != "svc-onboarding" || claims.Subject != "svc-onboarding" {
			t.Errorf("assertion does not name the machine user: %+v", claims)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-token-1", "token_type": "Bearer", "expires_in": expiresIn,
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestKeyTokenSourceExchangesASignedAssertionForAnAccessToken(t *testing.T) {
	raw, pub := machineKey(t, "svc-onboarding")
	var hits atomic.Int64
	srv := tokenEndpoint(t, pub, "https://auth.example", 3600, &hits)

	source, err := NewKeyTokenSource(raw, "https://auth.example", srv.URL+"/oauth/v2/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "access-token-1" {
		t.Fatalf("want the exchanged token, got %q", token)
	}
}

// A token per API call would turn one onboarding into dozens of exchanges, so
// it is held until shortly before it expires and then re-fetched.
func TestKeyTokenSourceReusesTheTokenUntilItNearlyExpires(t *testing.T) {
	raw, pub := machineKey(t, "svc-onboarding")
	var hits atomic.Int64
	srv := tokenEndpoint(t, pub, "https://auth.example", 120, &hits)

	source, err := NewKeyTokenSource(raw, "https://auth.example", srv.URL+"/oauth/v2/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	source.now = func() time.Time { return now }

	for range 3 {
		if _, err := source.Token(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("token was fetched %d times, want once", hits.Load())
	}

	now = now.Add(90 * time.Second)
	if _, err := source.Token(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("token was not refreshed near expiry: %d fetches", hits.Load())
	}
}

func TestKeyTokenSourceFailsClosedWhenTheAssertionIsRejected(t *testing.T) {
	raw, _ := machineKey(t, "svc-onboarding")
	_, other := machineKey(t, "svc-onboarding")
	var hits atomic.Int64
	srv := tokenEndpoint(t, other, "https://auth.example", 3600, &hits)

	source, err := NewKeyTokenSource(raw, "https://auth.example", srv.URL+"/oauth/v2/token", nil)
	if err != nil {
		t.Fatal(err)
	}
	if token, err := source.Token(context.Background()); err == nil {
		t.Fatalf("a rejected assertion produced a token: %q", token)
	}
}

func TestKeyTokenSourceRejectsAKeyItCannotUse(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":    "{",
		"no key":      `{"type":"serviceaccount","keyId":"key-1","userId":"svc"}`,
		"no user":     `{"type":"serviceaccount","keyId":"key-1","key":"-----BEGIN RSA PRIVATE KEY-----\nx\n-----END RSA PRIVATE KEY-----"}`,
		"not a key":   `{"type":"serviceaccount","keyId":"key-1","userId":"svc","key":"hunter2"}`,
		"empty input": "",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewKeyTokenSource([]byte(raw), "https://auth.example", "https://auth.example/oauth/v2/token", nil); err == nil {
				t.Fatal("an unusable machine key was accepted")
			}
		})
	}
}

func TestStaticTokenIsHandedBackUnchanged(t *testing.T) {
	token, err := StaticToken("pat-value").Token(context.Background())
	if err != nil || token != "pat-value" {
		t.Fatalf("got %q, %v", token, err)
	}
}
