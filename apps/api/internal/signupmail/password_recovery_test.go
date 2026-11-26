package signupmail

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestPasswordRecoveryMailIsPurposeBoundAndDeliveryIdempotent(t *testing.T) {
	file := t.TempDir() + "/key"
	if err := os.WriteFile(file, []byte("re_synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	id := uuid.Must(uuid.NewV7()).String()
	calls := 0
	c, err := New(file, "onboarding@helivanta.com", transport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body struct {
			Subject string   `json:"subject"`
			Text    string   `json:"text"`
			HTML    string   `json:"html"`
			To      []string `json:"to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if r.Header.Get("Idempotency-Key") != "helivanta-password-recovery-"+id || body.Subject != "Reset your password — Helivanta" || len(body.To) != 1 || body.To[0] != "owner@example.test" {
			t.Fatal("wrong recovery delivery binding")
		}
		for _, content := range []string{body.Text, body.HTML} {
			if !hasClosing(content, recoveryClosing) {
				t.Fatal("missing closing line")
			}
			for _, want := range []string{"12345678", "10 minutes", "two-step verification"} {
				if !strings.Contains(content, want) {
					t.Fatalf("missing recovery notice %q", want)
				}
			}
			for _, private := range []string{"tenantId", "userId", "signup/verify", id} {
				if strings.Contains(content, private) {
					t.Fatal("recovery message contains account or capability metadata")
				}
			}
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"synthetic-message"}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, err := c.SendPasswordRecovery(t.Context(), id, "owner@example.test", "12345678")
		if err != nil || got != "synthetic-message" {
			t.Fatal("delivery not acknowledged", err)
		}
	}
	if calls != 2 {
		t.Fatal("delivery not exercised")
	}
	for _, tc := range []struct{ id, email, code string }{
		{uuid.NewString(), "owner@example.test", "12345678"},
		{id, "Name <owner@example.test>", "12345678"},
		{id, "owner@example.test", "1234567x"},
		{id, "owner@example.test", "1234567"},
	} {
		if _, err := c.SendPasswordRecovery(t.Context(), tc.id, tc.email, tc.code); err == nil {
			t.Fatal("invalid recovery delivery admitted")
		}
	}
	if calls != 2 {
		t.Fatal("invalid input reached provider")
	}
}
