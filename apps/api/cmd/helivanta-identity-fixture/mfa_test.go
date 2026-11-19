//go:build identityfixture

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFixtureMFAProvisioningAddsOnlyTwoCanonicalTenantScopes(t *testing.T) {
	baseline, enabled := fixtureTenantIDs(false), fixtureTenantIDs(true)
	if len(baseline) != 2 || len(enabled) != 4 || baseline[0] != enabled[0] || baseline[1] != enabled[1] {
		t.Fatal("MFA opt-in must preserve baseline scopes and add exactly two isolated tenants")
	}
	for _, id := range enabled[2:] {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.Variant() != uuid.RFC4122 || parsed.Version() != 7 || parsed.String() != id {
			t.Fatal("native MFA tenant fixtures must satisfy the canonical workload scope")
		}
	}
}

func TestFixtureMFACommandsRequireSeparateExplicitOptInAndExactScope(t *testing.T) {
	const base = "/v1/orgs/11111111-1111-7111-8111-111111111111/staff-mfa-recoveries/019f39c2-1738-7998-b5de-6d4214f614e0"
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	for _, enabled := range []bool{false, true} {
		handler := fixtureMFAHandler(next, enabled)
		for _, pair := range [][2]string{{"GET", "/v1/orgs/11111111-1111-1111-1111-111111111111/identity"}, {"GET", "/v1/orgs/22222222-2222-2222-2222-222222222222/identity"}, {"GET", "/v1/orgs/11111111-1111-7111-8111-111111111111/identity"}, {"GET", "/v1/orgs/22222222-2222-7222-8222-222222222222/identity"}, {"PUT", base}, {"GET", base}, {"POST", base + "/dispatch"}, {"POST", base + "/cancel"}, {"POST", "/internal/v1/customer-signups/staff-mfa-recovery/admit"}} {
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, httptest.NewRequest(pair[0], pair[1], nil))
			want := 404
			if enabled {
				want = 204
			}
			if out.Code != want {
				t.Fatalf("exact native MFA command opt-in failed: %s %d", pair[0], out.Code)
			}
		}
		for _, tenant := range []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"} {
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, httptest.NewRequest("PUT", "/v1/orgs/"+tenant+"/staff-mfa-recoveries/019f39c2-1738-7998-b5de-6d4214f614e0", nil))
			if out.Code != 404 {
				t.Fatal("legacy sample identifiers must not be used for native MFA commands")
			}
		}
		for _, pair := range [][2]string{{"GET", "/v1/orgs/33333333-3333-4333-8333-333333333333/identity"}, {"POST", "/v1/orgs/11111111-1111-1111-1111-111111111111/identity"}, {"DELETE", base}, {"POST", base}, {"PUT", base + "/extra"}, {"GET", base + "/dispatch"}, {"POST", base + "/delete"}, {"PUT", "/v1/orgs/33333333-3333-4333-8333-333333333333/staff-mfa-recoveries/019f39c2-1738-7998-b5de-6d4214f614e0"}, {"PUT", "/v1/products"}, {"GET", "/internal/v1/customer-signups/staff-mfa-recovery/admit"}} {
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, httptest.NewRequest(pair[0], pair[1], nil))
			if out.Code != 404 {
				t.Fatal("MFA fixture widened unrelated authority")
			}
		}
	}
}

func TestFixtureRequiresRealActiveNativeMachineProof(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		ok         bool
	}{
		{"machine", `{"user":{"userId":"native-machine","state":"USER_STATE_ACTIVE","details":{"resourceOwner":"platform-org"},"machine":{"name":"Local product"}}}`, 200, true},
		{"human", `{"user":{"userId":"native-machine","state":"USER_STATE_ACTIVE","details":{"resourceOwner":"platform-org"},"human":{"profile":{"displayName":"Human"}}}}`, 200, false},
		{"foreign", `{"user":{"userId":"native-machine","state":"USER_STATE_ACTIVE","details":{"resourceOwner":"foreign-org"},"machine":{"name":"Local product"}}}`, 200, false},
		{"inactive", `{"user":{"userId":"native-machine","state":"USER_STATE_INACTIVE","details":{"resourceOwner":"platform-org"},"machine":{"name":"Local product"}}}`, 200, false},
		{"wrong_subject", `{"user":{"userId":"other-machine","state":"USER_STATE_ACTIVE","details":{"resourceOwner":"platform-org"},"machine":{"name":"Local product"}}}`, 200, false},
		{"outage", `{}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: fixtureMachineTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() != localIssuer+"/v2/users/native-machine" || r.Header.Get("Authorization") != "Bearer synthetic-fixture-token" {
					t.Fatal("native machine proof escaped pinned fixture")
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			err := verifyFixtureMachine(t.Context(), client, "synthetic-fixture-token", "native-machine", "platform-org")
			if (err == nil) != tc.ok {
				t.Fatal("native fixture machine eligibility not proven")
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-fixture-token") {
				t.Fatal("credential leaked")
			}
		})
	}
}

type fixtureMachineTransport func(*http.Request) (*http.Response, error)

func (f fixtureMachineTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
