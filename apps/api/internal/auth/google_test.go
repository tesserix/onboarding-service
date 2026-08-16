package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGoogleAuthorizeURLCarriesPKCEAndNonce(t *testing.T) {
	c := NewGoogleClient("client-1", "secret-1", "https://onboard.tesserix.app/api/auth/callback")

	raw := c.AuthorizeURL("state-1", "nonce-1", "challenge-1")
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "accounts.google.com" {
		t.Fatalf("sign-in goes to %q, want accounts.google.com", u.Host)
	}
	q := u.Query()
	for field, want := range map[string]string{
		"client_id":             "client-1",
		"redirect_uri":          "https://onboard.tesserix.app/api/auth/callback",
		"response_type":         "code",
		"state":                 "state-1",
		"nonce":                 "nonce-1",
		"code_challenge":        "challenge-1",
		"code_challenge_method": "S256",
	} {
		if q.Get(field) != want {
			t.Errorf("%s = %q, want %q", field, q.Get(field), want)
		}
	}
	if !strings.Contains(q.Get("scope"), "email") || !strings.Contains(q.Get("scope"), "openid") {
		t.Errorf("scope = %q, want openid and email", q.Get("scope"))
	}
	// The client secret is a credential, not a query parameter.
	if strings.Contains(raw, "secret-1") {
		t.Fatal("the client secret leaked into the authorize URL")
	}
}

// Google requires the client secret on a web-application client; PKCE alone is
// rejected, so the exchange must send both.
func TestGoogleExchangeSendsTheSecretAndTheVerifier(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": "id-token-1"})
	}))
	t.Cleanup(srv.Close)

	c := NewGoogleClient("client-1", "secret-1", "https://onboard.tesserix.app/api/auth/callback")
	c.TokenEndpoint = srv.URL

	tokens, err := c.Exchange(context.Background(), "code-1", "verifier-1")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.IDToken != "id-token-1" {
		t.Fatalf("id token = %q", tokens.IDToken)
	}
	for field, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "code-1",
		"code_verifier": "verifier-1",
		"client_id":     "client-1",
		"client_secret": "secret-1",
	} {
		if form.Get(field) != want {
			t.Errorf("%s = %q, want %q", field, form.Get(field), want)
		}
	}
}
