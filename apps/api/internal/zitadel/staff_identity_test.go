package zitadel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCreateStaffIdentityCreatesOnlyAnUnverifiedRoleFreeHuman(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/v2/users/new" || r.Header.Get("Authorization") != "Bearer TEST-source-token" || r.Header.Get("x-zitadel-orgid") != "org-1" {
			t.Error("creation did not use the exact scoped provider contract")
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if len(body) != 5 || string(body["organizationId"]) != `"org-1"` || string(body["userId"]) != `"staff-test-account"` || string(body["username"]) != `"synthetic@example.invalid"` {
			t.Error("creation facts were not bound to the prepared command")
		}
		var human struct {
			Profile map[string]string          `json:"profile"`
			Email   map[string]json.RawMessage `json:"email"`
		}
		if err := json.Unmarshal(body["human"], &human); err != nil {
			t.Error(err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(body["human"], &fields); err != nil || len(fields) != 2 || len(human.Email) != 1 || string(human.Email["email"]) != `"synthetic@example.invalid"` || human.Profile["givenName"] != "Synthetic" || human.Profile["familyName"] != "Staff" {
			t.Error("creation supplied credentials, verification, or unintended human fields")
		}
		var metadata []struct{ Key, Value string }
		if err := json.Unmarshal(body["metadata"], &metadata); err != nil || len(metadata) != 1 || metadata[0].Key != "onboarding_staff_command" || metadata[0].Value != base64.StdEncoding.EncodeToString([]byte("TEST-binding-nonce")) {
			t.Error("creation lost its durable-command correlation")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"staff-test-account","creationDate":"2026-10-30T10:00:00Z"}`))
	}))
	t.Cleanup(server.Close)
	client := New(server.URL, StaticToken("TEST-source-token"), "platform-1", nil)
	client.http = server.Client()
	got, err := client.CreateStaffIdentity(context.Background(), StaffIdentityInput{
		OrganizationID: "org-1", UserID: "staff-test-account", Email: "synthetic@example.invalid",
		GivenName: "Synthetic", FamilyName: "Staff", BindingNonce: "TEST-binding-nonce",
	})
	if err != nil || got.ID != "staff-test-account" || !got.CreatedAt.Equal(time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)) || calls.Load() != 1 {
		t.Fatalf("role-free creation result: id=%q calls=%d err=%v", got.ID, calls.Load(), err)
	}
}

func TestCreateStaffIdentityNeverRetriesOrDisclosesUncertainProviderResults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"conflict", 409, `PRIVATE_EMAIL PRIVATE_CODE`},
		{"unavailable", 503, `PRIVATE_EMAIL PRIVATE_CODE`},
		{"throttled", 429, `PRIVATE_EMAIL PRIVATE_CODE`},
		{"wrong ID", 200, `{"id":"other-account","creationDate":"2026-10-30T10:00:00Z"}`},
		{"missing timestamp", 200, `{"id":"staff-test-account"}`},
		{"invalid timestamp", 200, `{"id":"staff-test-account","creationDate":"unknown"}`},
		{"unexpected code", 200, `{"id":"staff-test-account","creationDate":"2026-10-30T10:00:00Z","emailCode":"PRIVATE_CODE"}`},
		{"second document", 200, `{"id":"staff-test-account","creationDate":"2026-10-30T10:00:00Z"}{}`},
		{"oversized", 200, strings.Repeat(" ", identityReadLimit+1)},
		{"empty", 200, ""},
		{"null", 200, "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(server.Close)
			client := New(server.URL, StaticToken("TEST-source-token"), "platform-1", nil)
			client.http = server.Client()
			got, err := client.CreateStaffIdentity(context.Background(), StaffIdentityInput{
				OrganizationID: "org-1", UserID: "staff-test-account", Email: "synthetic@example.invalid",
				GivenName: "Synthetic", FamilyName: "Staff", BindingNonce: "TEST-binding-nonce",
			})
			if !errors.Is(err, ErrStaffIdentityUncertain) || got.ID != "" || !got.CreatedAt.IsZero() || calls.Load() != 1 {
				t.Fatal("uncertain creation was retried or reported successful")
			}
			for _, private := range []string{"PRIVATE_EMAIL", "PRIVATE_CODE", "TEST-source-token"} {
				if strings.Contains(fmt.Sprint(err), private) {
					t.Fatal("provider payload escaped in error")
				}
			}
		})
	}
}

func TestCreateStaffIdentityRejectsControlCharactersBeforeDispatch(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	client := New(server.URL, StaticToken("TEST-source-token"), "platform-1", nil)
	client.http = server.Client()
	_, err := client.CreateStaffIdentity(context.Background(), StaffIdentityInput{
		OrganizationID: "org-1", UserID: "staff-test-account", Email: "synthetic@example.invalid",
		GivenName: "Synthetic\nInjected", FamilyName: "Staff", BindingNonce: "TEST-binding-nonce",
	})
	if !errors.Is(err, ErrStaffIdentityInvalid) || calls.Load() != 0 {
		t.Fatal("invalid person name reached the provider")
	}
}

func TestCreateStaffIdentityRejectsAmbiguousCreationProof(t *testing.T) {
	for _, body := range []string{
		`{"id":"other-account","id":"staff-test-account","creationDate":"2026-10-30T10:00:00Z"}`,
		`{"id":"staff-test-account","creationDate":"2026-10-30T10:00:00Z","creationDate":"2026-10-30T11:00:00Z"}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			t.Cleanup(server.Close)
			client := New(server.URL, StaticToken("TEST-source-token"), "platform-1", nil)
			client.http = server.Client()
			got, err := client.CreateStaffIdentity(context.Background(), StaffIdentityInput{
				OrganizationID: "org-1", UserID: "staff-test-account", Email: "synthetic@example.invalid",
				GivenName: "Synthetic", FamilyName: "Staff", BindingNonce: "TEST-binding-nonce",
			})
			if !errors.Is(err, ErrStaffIdentityUncertain) || got.ID != "" || !got.CreatedAt.IsZero() {
				t.Fatal("ambiguous provider proof was accepted as creation")
			}
		})
	}
}

func TestCreateStaffIdentityDoesNotFollowRedirects(t *testing.T) {
	var targetCalls, sourceCalls atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(target.Close)
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceCalls.Add(1)
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)
	client := New(source.URL, StaticToken("TEST-source-token"), "platform-1", nil)
	client.http = source.Client()
	got, err := client.CreateStaffIdentity(context.Background(), StaffIdentityInput{
		OrganizationID: "org-1", UserID: "staff-test-account", Email: "synthetic@example.invalid",
		GivenName: "Synthetic", FamilyName: "Staff", BindingNonce: "TEST-binding-nonce",
	})
	if !errors.Is(err, ErrStaffIdentityUncertain) || got.ID != "" || sourceCalls.Load() != 1 || targetCalls.Load() != 0 {
		t.Fatal("creation redirect was followed or reported successful")
	}
}

func TestCreateStaffIdentityRejectsInvalidImmutableInputWithoutProviderCalls(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	client := New(server.URL, StaticToken("TEST-source-token"), "platform-1", nil)
	client.http = server.Client()
	for _, mutate := range []func(*StaffIdentityInput){
		func(in *StaffIdentityInput) { in.OrganizationID = "" },
		func(in *StaffIdentityInput) { in.OrganizationID = "org/other" },
		func(in *StaffIdentityInput) { in.UserID = "" },
		func(in *StaffIdentityInput) { in.Email = "Display <synthetic@example.invalid>" },
		func(in *StaffIdentityInput) { in.Email = "invalid" },
		func(in *StaffIdentityInput) { in.GivenName = " " },
		func(in *StaffIdentityInput) { in.FamilyName = strings.Repeat("a", 201) },
		func(in *StaffIdentityInput) { in.BindingNonce = "short" },
	} {
		in := StaffIdentityInput{OrganizationID: "org-1", UserID: "staff-test-account", Email: "synthetic@example.invalid", GivenName: "Synthetic", FamilyName: "Staff", BindingNonce: "TEST-binding-nonce"}
		mutate(&in)
		got, err := client.CreateStaffIdentity(context.Background(), in)
		if !errors.Is(err, ErrStaffIdentityInvalid) || got.ID != "" || calls.Load() != 0 {
			t.Fatal("invalid immutable input reached the provider")
		}
		if strings.Contains(fmt.Sprint(in), "example.invalid") || strings.Contains(fmt.Sprint(in), "TEST-binding-nonce") {
			t.Fatal("input formatting disclosed private fields")
		}
	}
}
