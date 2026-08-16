package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// OIDCClient runs the authorization-code flow for console humans. Endpoints are
// explicit rather than derived from an issuer: Google does not serve them under
// its issuer host.
type OIDCClient struct {
	AuthorizeEndpoint string
	TokenEndpoint     string
	ClientID          string
	ClientSecret      string
	RedirectURI       string
	Scopes            []string
	HTTP              *http.Client
}

type Tokens struct {
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
}

func NewPKCE() (verifier, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("pkce verifier: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:]), nil
}

func (c *OIDCClient) AuthorizeURL(state, nonce, challenge string) string {
	q := url.Values{
		"client_id":             {c.ClientID},
		"redirect_uri":          {c.RedirectURI},
		"response_type":         {"code"},
		"scope":                 {strings.Join(c.Scopes, " ")},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return c.AuthorizeEndpoint + "?" + q.Encode()
}

func (c *OIDCClient) Exchange(ctx context.Context, code, verifier string) (Tokens, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.RedirectURI},
		"client_id":     {c.ClientID},
		"code_verifier": {verifier},
	}
	// Google rejects PKCE alone on a web-application client.
	if c.ClientSecret != "" {
		form.Set("client_secret", c.ClientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return Tokens{}, fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		// The body may echo the code; the status is all the caller needs.
		return Tokens{}, fmt.Errorf("token exchange: issuer returned %d", resp.StatusCode)
	}
	var tokens Tokens
	if err := json.Unmarshal(body, &tokens); err != nil {
		return Tokens{}, fmt.Errorf("decode token response: %w", err)
	}
	if tokens.IDToken == "" {
		return Tokens{}, fmt.Errorf("token exchange: no id_token in response")
	}
	return tokens, nil
}
