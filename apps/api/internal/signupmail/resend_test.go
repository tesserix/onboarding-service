package signupmail

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestResendUsesMountedKeyNativeCodeFragmentAndEscapedContent(t *testing.T) {
	keyFile := t.TempDir() + "/api-key"
	if err := os.WriteFile(keyFile, []byte("re_synthetic_key"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := New(keyFile, "onboarding@helivanta.app", nil)
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.resend.com/emails" || r.Header.Get("Authorization") != "Bearer re_synthetic_key" || r.Header.Get("Idempotency-Key") == "" {
			t.Fatal("incorrect provider authentication or destination")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["from"] != "Helivanta <onboarding@helivanta.app>" || body["to"].([]any)[0] != "owner@example.test" {
			t.Fatal("incorrect email envelope")
		}
		html := body["html"].(string)
		for _, content := range []string{html, body["text"].(string)} {
			if !strings.Contains(content, "expires after 1 hour") || !strings.Contains(content, "3 emails per hour and 10 per 24 hours") || !strings.Contains(content, "verified") || !strings.Contains(content, "Across workspaces") || !strings.Contains(content, "30 per 24 hours") {
				t.Fatal("mail omitted expiry or resend limits")
			}
		}
		if strings.Contains(html, "<script>") || !strings.Contains(html, "&lt;script&gt;") || !strings.Contains(html, "https://helivanta.app/signup/verify#") || !strings.Contains(html, "phase=email") || !strings.Contains(html, "code=native-code") {
			t.Fatal("unsafe content or missing native link")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"provider-message"}`))}, nil
	})
	id, err := client.Send(context.Background(), Message{Email: "owner@example.test", Name: "<script>", Organization: "Hospital", UserID: "owner-test", Code: "native-code", Phase: "email"})
	if err != nil || id != "provider-message" {
		t.Fatalf("acceptance missing: %s %v", id, err)
	}
}

func TestResendFailuresNeverExposeProviderBodiesOrSecrets(t *testing.T) {
	for _, response := range []string{`{"id":""}`, `not json`, strings.Repeat("x", 9000)} {
		t.Run(response[:min(8, len(response))], func(t *testing.T) {
			keyFile := t.TempDir() + "/api-key"
			if err := os.WriteFile(keyFile, []byte("re_synthetic_key"), 0600); err != nil {
				t.Fatal(err)
			}
			client, err := New(keyFile, "onboarding@helivanta.app", nil)
			if err != nil {
				t.Fatal(err)
			}
			client.http.Transport = transport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(response))}, nil
			})
			if _, err := client.Send(t.Context(), Message{Email: "owner@example.test", UserID: "owner-test", Code: "native-code", Phase: "password"}); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("malformed provider acceptance: %v", err)
			}
		})
	}
}

func TestResendRejectsUntrustedSenderAndUnsafeInput(t *testing.T) {
	for _, sender := range []string{"", "attacker@example.test", "Name <onboarding@helivanta.app>", "onboarding@helivanta.app\r\nBcc:a@b.test"} {
		if _, err := New("/key", sender, nil); err == nil {
			t.Fatal("unsafe sender accepted")
		}
	}
	keyFile := t.TempDir() + "/api-key"
	if err := os.WriteFile(keyFile, []byte("re_synthetic_key"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := New(keyFile, "onboarding@helivanta.app", nil)
	if err != nil {
		t.Fatal(err)
	}
	client.http.Transport = transport(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid input reached provider")
		return nil, nil
	})
	for _, message := range []Message{
		{Email: "owner@example.test", UserID: "owner-test", Code: "code", Phase: "foreign"},
		{Email: "Owner <owner@example.test>", UserID: "owner-test", Code: "code", Phase: "email"},
		{Email: "owner@example.test", UserID: "../foreign", Code: "code", Phase: "email"},
		{Email: "owner@example.test", UserID: "owner-test", Code: "", Phase: "email"},
	} {
		if _, err := client.Send(t.Context(), message); err == nil {
			t.Fatal("invalid message accepted")
		}
	}
}

func TestResendReadsRotatedKeyAndSanitizesProviderRefusal(t *testing.T) {
	file := t.TempDir() + "/key"
	if err := os.WriteFile(file, []byte("re_first"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := New(file, "onboarding@helivanta.app", nil)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	ids := []string{}
	client.http.Transport = transport(func(r *http.Request) (*http.Response, error) {
		keys = append(keys, r.Header.Get("Authorization"))
		ids = append(ids, r.Header.Get("Idempotency-Key"))
		return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"error":"sensitive-provider-body"}`))}, nil
	})
	message := Message{Email: "owner@example.test", UserID: "owner-test", Code: "native-code", Phase: "email"}
	for _, key := range []string{"re_first", "re_second"} {
		if err := os.WriteFile(file, []byte(key), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Send(t.Context(), message); err != ErrUnavailable || strings.Contains(err.Error(), "sensitive") {
			t.Fatal("provider refusal leaked or succeeded")
		}
	}
	if len(keys) != 2 || keys[0] != "Bearer re_first" || keys[1] != "Bearer re_second" || ids[0] == ids[1] {
		t.Fatal("rotation or regenerated-payload retry key failed")
	}
	if err := os.WriteFile(file, []byte("invalid\r\nkey"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Send(t.Context(), message); err != ErrUnavailable || len(keys) != 2 {
		t.Fatal("invalid mounted key reached provider")
	}
}
