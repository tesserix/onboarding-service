package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestAuthorizeURLCarriesPKCEAndState(t *testing.T) {
	c := &OIDCClient{Issuer: "https://auth.tesserix.app", ClientID: "console", RedirectURI: "https://onboard.tesserix.app/api/auth/callback"}

	raw := c.AuthorizeURL("state-1", "nonce-1", "challenge-1")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge") != "challenge-1" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("PKCE missing: %v", q)
	}
	if q.Get("state") != "state-1" || q.Get("nonce") != "nonce-1" {
		t.Fatalf("state/nonce missing: %v", q)
	}
	if q.Get("response_type") != "code" {
		t.Fatalf("not the authorization-code flow: %v", q)
	}
	if !strings.Contains(q.Get("scope"), "openid") {
		t.Fatalf("scope %q", q.Get("scope"))
	}
}

func TestExchangeSendsTheVerifierAndReturnsTheIDToken(t *testing.T) {
	var form url.Values
	srv := fakeIssuer(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]any{"id_token": "id-tok", "access_token": "acc-tok"})
	})
	c := &OIDCClient{Issuer: srv.URL, ClientID: "console", RedirectURI: "https://onboard.tesserix.app/cb", HTTP: srv.Client()}

	tokens, err := c.Exchange(context.Background(), "the-code", "the-verifier")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.IDToken != "id-tok" {
		t.Fatalf("id token %q", tokens.IDToken)
	}
	if form.Get("code_verifier") != "the-verifier" {
		t.Fatalf("verifier not sent: %v", form)
	}
	if form.Get("grant_type") != "authorization_code" {
		t.Fatalf("grant type %q", form.Get("grant_type"))
	}
}

func TestExchangeSurfacesAnIssuerError(t *testing.T) {
	srv := fakeIssuer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	})
	c := &OIDCClient{Issuer: srv.URL, ClientID: "console", RedirectURI: "https://x/cb", HTTP: srv.Client()}

	if _, err := c.Exchange(context.Background(), "code", "verifier"); err == nil {
		t.Fatal("invalid_grant reported as success")
	}
}
