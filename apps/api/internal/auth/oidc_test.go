package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func fakeIssuer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestPKCEChallengeIsTheS256OfTheVerifier(t *testing.T) {
	verifier, challenge, err := NewPKCE()
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(verifier))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); challenge != want {
		t.Fatalf("challenge %q, want %q", challenge, want)
	}
	if len(verifier) < 43 {
		t.Fatalf("verifier too short to resist guessing: %d chars", len(verifier))
	}
}

func TestExchangeSurfacesAnIssuerError(t *testing.T) {
	srv := fakeIssuer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	c := NewGoogleClient("client-1", "secret-1", "https://x/cb")
	c.TokenEndpoint = srv.URL
	c.HTTP = srv.Client()

	if _, err := c.Exchange(context.Background(), "code", "verifier"); err == nil {
		t.Fatal("invalid_grant reported as success")
	}
}
