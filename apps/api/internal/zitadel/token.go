package zitadel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenSource is the credential this service presents to Zitadel. The machine
// key is the real one; StaticToken exists for the break-glass PAT, which is an
// instance-wide credential that cannot be scoped down or expired.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

type StaticToken string

func (s StaticToken) Token(context.Context) (string, error) {
	if s == "" {
		return "", errors.New("zitadel: no token configured")
	}
	return string(s), nil
}

// refreshMargin re-fetches before the token actually expires, so a call that
// starts just under the wire does not arrive just over it.
const refreshMargin = 60 * time.Second

// KeyTokenSource exchanges a private-key JWT for a short-lived access token
// (RFC 7523). Nothing long-lived crosses the network and the credential can be
// revoked by deleting the key in Zitadel.
type KeyTokenSource struct {
	keyID    string
	userID   string
	issuer   string
	tokenURL string
	signer   any
	http     *http.Client
	log      *slog.Logger
	now      func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

type machineKeyFile struct {
	Type   string `json:"type"`
	KeyID  string `json:"keyId"`
	Key    string `json:"key"`
	UserID string `json:"userId"`
}

func NewKeyTokenSource(raw []byte, issuer, tokenURL string, log *slog.Logger) (*KeyTokenSource, error) {
	if log == nil {
		log = slog.Default()
	}
	var file machineKeyFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, fmt.Errorf("zitadel: machine key is not valid json: %w", err)
	}
	if file.KeyID == "" || file.UserID == "" || file.Key == "" {
		return nil, errors.New("zitadel: machine key needs keyId, userId and key")
	}
	signer, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(file.Key))
	if err != nil {
		return nil, fmt.Errorf("zitadel: machine key is not an RSA private key: %w", err)
	}
	return &KeyTokenSource{
		keyID: file.KeyID, userID: file.UserID,
		issuer:   strings.TrimSuffix(issuer, "/"),
		tokenURL: tokenURL,
		signer:   signer,
		http:     &http.Client{Timeout: 15 * time.Second},
		log:      log,
		now:      time.Now,
	}, nil
}

func (k *KeyTokenSource) Token(ctx context.Context) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.token != "" && k.now().Before(k.expires.Add(-refreshMargin)) {
		return k.token, nil
	}

	assertion, err := k.assertion()
	if err != nil {
		return "", err
	}
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
		"scope":      {"openid urn:zitadel:iam:org:project:id:zitadel:aud"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, k.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("zitadel: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := k.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("zitadel: token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 400 {
		// The body carries an OAuth error code, never the assertion.
		return "", fmt.Errorf("zitadel: token exchange rejected: %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("zitadel: decode token response: %w", err)
	}
	if out.AccessToken == "" {
		return "", errors.New("zitadel: token exchange returned no access token")
	}

	lifetime := time.Duration(out.ExpiresIn) * time.Second
	if lifetime <= refreshMargin {
		lifetime = 2 * refreshMargin
	}
	k.token = out.AccessToken
	k.expires = k.now().Add(lifetime)
	return k.token, nil
}

func (k *KeyTokenSource) assertion() (string, error) {
	now := k.now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    k.userID,
		Subject:   k.userID,
		Audience:  jwt.ClaimStrings{k.issuer},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	})
	token.Header["kid"] = k.keyID
	signed, err := token.SignedString(k.signer)
	if err != nil {
		return "", fmt.Errorf("zitadel: sign assertion: %w", err)
	}
	return signed, nil
}
