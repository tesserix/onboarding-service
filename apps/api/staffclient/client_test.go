package staffclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

type tokenSource func(context.Context) (string, error)

func (f tokenSource) Token(ctx context.Context) (string, error) { return f(ctx) }

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestClientBoundsResponsesAndDoesNotExposeProviderCredentials(t *testing.T) {
	scope := Scope{TenantID: uuid.New(), CommandID: uuid.Must(uuid.NewV7()), ActorSubject: "TEST-owner"}
	for _, tc := range []struct {
		name, body, retry string
		status            int
	}{
		{"redirect", "private-provider-body", "", 302},
		{"wrong credentials", "private-provider-body", "", 401},
		{"not found", "private-provider-body", "", 404},
		{"stale", "private-provider-body", "", 409},
		{"budget", "private-provider-body", "60", 429},
		{"unsafe header", "private-provider-body", "99999999", 429},
		{"malformed", "private-provider-body", "", 200},
		{"oversized", strings.Repeat(" ", 32769), "", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, err := New(Config{Endpoint: "http://onboarding-api.onboarding.svc.cluster.local:8080", ProductKey: "helivanta", Tokens: tokenSource(func(context.Context) (string, error) { return "private-access-token", nil }), HTTP: &http.Client{Transport: responseTransport(func(req *http.Request) (*http.Response, error) {
				if req.Header.Get("X-Expected-Product-Key") != "helivanta" || req.Header.Get("Cookie") != "" || !strings.HasSuffix(req.URL.RawQuery, "actor_subject=TEST-owner") {
					t.Error("private request binding lost")
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{"Retry-After": {tc.retry}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = client.Get(t.Context(), scope); err == nil || strings.Contains(err.Error(), "private-") {
				t.Fatal("unverified response accepted or private data exposed")
			}
		})
	}
	client, err := New(Config{Endpoint: "http://onboarding-api.onboarding.svc.cluster.local:8080", ProductKey: "helivanta", Tokens: tokenSource(func(context.Context) (string, error) { return "", errors.New("private-credential-failure") })})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.Get(t.Context(), scope); !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "private-") {
		t.Fatal("token source failure disclosed credentials")
	}
	for _, endpoint := range []string{"https://evil.example", "http://localhost:8080", "http://onboarding-api.onboarding.svc.cluster.local:8080/path", "http://onboarding-api.onboarding.svc.cluster.local:8080?x=1"} {
		if _, err = New(Config{Endpoint: endpoint, ProductKey: "helivanta", Tokens: client.tokens}); err == nil {
			t.Fatal("untrusted owner destination accepted")
		}
	}
}
