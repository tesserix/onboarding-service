package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

var ErrInvalidSession = errors.New("invalid session")

// Session is what a human's cookie carries. It is sealed, not signed, so none
// of it is readable by the browser.
type Session struct {
	Subject   string    `json:"sub"`
	Email     string    `json:"email"`
	OrgID     string    `json:"org,omitempty"`
	Roles     []string  `json:"roles,omitempty"`
	CSRF      string    `json:"csrf"`
	ExpiresAt time.Time `json:"exp"`
}

type SessionCodec struct {
	aead cipher.AEAD
}

func NewSessionCodec(key []byte) (*SessionCodec, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("session key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("session cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("session aead: %w", err)
	}
	return &SessionCodec{aead: aead}, nil
}

func (c *SessionCodec) Encode(s Session) (string, error) {
	plain, err := json.Marshal(s)
	if err != nil {
		return "", fmt.Errorf("encode session: %w", err)
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("session nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(c.aead.Seal(nonce, nonce, plain, nil)), nil
}

func (c *SessionCodec) Decode(value string) (Session, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(raw) <= c.aead.NonceSize() {
		return Session{}, ErrInvalidSession
	}
	nonce, sealed := raw[:c.aead.NonceSize()], raw[c.aead.NonceSize():]
	plain, err := c.aead.Open(nil, nonce, sealed, nil)
	if err != nil {
		return Session{}, ErrInvalidSession
	}

	var s Session
	if err := json.Unmarshal(plain, &s); err != nil {
		return Session{}, ErrInvalidSession
	}
	if time.Now().After(s.ExpiresAt) {
		return Session{}, fmt.Errorf("%w: expired", ErrInvalidSession)
	}
	return s, nil
}

// NewCSRFToken is also used for the OIDC state and nonce values.
func NewCSRFToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("csrf token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// SameCSRF is constant-time, and an empty token never matches — an absent
// header must not pass against an absent session value.
func SameCSRF(fromSession, fromRequest string) bool {
	if fromSession == "" || fromRequest == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(fromSession), []byte(fromRequest)) == 1
}
