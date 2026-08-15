package auth

import (
	"strings"
	"testing"
	"time"
)

func newCodec(t *testing.T) *SessionCodec {
	t.Helper()
	c, err := NewSessionCodec(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func aSession() Session {
	return Session{
		Subject:   "user-1",
		Email:     "sam@tesserix.app",
		OrgID:     "org-123",
		Roles:     []string{"platform-admin"},
		CSRF:      "csrf-token",
		ExpiresAt: time.Now().Add(time.Hour).Truncate(time.Second),
	}
}

func TestSessionRoundTrips(t *testing.T) {
	c := newCodec(t)
	encoded, err := c.Encode(aSession())
	if err != nil {
		t.Fatal(err)
	}

	got, err := c.Decode(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "user-1" || got.OrgID != "org-123" || got.CSRF != "csrf-token" ||
		len(got.Roles) != 1 || got.Roles[0] != "platform-admin" {
		t.Fatalf("session not round-tripped: %+v", got)
	}
}

func TestAShortKeyIsRefused(t *testing.T) {
	if _, err := NewSessionCodec(make([]byte, 16)); err == nil {
		t.Fatal("accepted a 128-bit session key")
	}
}

// The cookie is AEAD-sealed, so the claims inside it are neither readable nor
// editable by the browser holding it.
func TestATamperedCookieIsRejected(t *testing.T) {
	c := newCodec(t)
	encoded, err := c.Encode(aSession())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(encoded, "sam@tesserix.app") {
		t.Fatal("session contents are readable in the cookie")
	}

	tampered := []byte(encoded)
	tampered[len(tampered)-2] ^= 'x'
	if _, err := c.Decode(string(tampered)); err == nil {
		t.Fatal("tampered cookie accepted")
	}
}

func TestACookieFromAnotherKeyIsRejected(t *testing.T) {
	encoded, err := newCodec(t).Encode(aSession())
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewSessionCodec([]byte("an entirely different 32b key!!!"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Decode(encoded); err == nil {
		t.Fatal("cookie sealed with another key accepted")
	}
}

// Expiry is inside the sealed payload, not in the cookie's Max-Age: the client
// controls the latter.
func TestAnExpiredSessionIsRejected(t *testing.T) {
	c := newCodec(t)
	s := aSession()
	s.ExpiresAt = time.Now().Add(-time.Minute)
	encoded, err := c.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Decode(encoded); err == nil {
		t.Fatal("expired session accepted")
	}
}

func TestEncodingTheSameSessionTwiceDiffers(t *testing.T) {
	c := newCodec(t)
	first, _ := c.Encode(aSession())
	second, _ := c.Encode(aSession())
	if first == second {
		t.Fatal("nonce reused")
	}
}

func TestCSRFComparisonRejectsAMismatch(t *testing.T) {
	if !SameCSRF("abc", "abc") {
		t.Fatal("identical tokens rejected")
	}
	for _, other := range []string{"", "abd", "ab", "abcd"} {
		if SameCSRF("abc", other) {
			t.Fatalf("accepted %q as a match for abc", other)
		}
	}
	if SameCSRF("", "") {
		t.Fatal("an empty CSRF token matched itself")
	}
}
