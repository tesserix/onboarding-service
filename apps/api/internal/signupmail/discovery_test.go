package signupmail

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestDiscoveryMailContainsOnlyCodeAndBoundedExpiry(t *testing.T) {
	file := t.TempDir() + "/key"
	if os.WriteFile(file, []byte("re_synthetic"), 0600) != nil {
		t.Fatal("key fixture")
	}
	c, err := New(file, "onboarding@helivanta.app", transport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("bad payload")
		}
		if r.Header.Get("Authorization") != "Bearer re_synthetic" || body["subject"] != "Your Helivanta sign-in code" {
			t.Fatal("wrong message")
		}
		for _, field := range []string{"text", "html"} {
			text := body[field].(string)
			if !strings.Contains(text, "12345678") || !strings.Contains(text, "10 minutes") || strings.Contains(text, "tenantId") || strings.Contains(text, "signup/verify") {
				t.Fatal("invalid discovery content")
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"synthetic-message"}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.SendDiscovery(t.Context(), "owner@example.test", "12345678"); err != nil {
		t.Fatal(err)
	}
}
