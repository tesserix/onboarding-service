package zitadel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func staffObservationReplies() map[string]string {
	return map[string]string{
		"/v2/users/TEST-staff-account": `{"user":{"userId":"TEST-staff-account","state":"USER_STATE_ACTIVE","details":{"sequence":"101","resourceOwner":"org-1","creationDate":"2026-10-30T10:00:00.123456789Z"},"human":{"email":{"email":"synthetic@example.invalid","isVerified":true}}}}`,
		"/management/v1/users/TEST-staff-account/metadata/onboarding_staff_command": `{"metadata":{"key":"onboarding_staff_command","value":"` + base64.StdEncoding.EncodeToString([]byte("TEST-binding-nonce")) + `","details":{"sequence":"102","resourceOwner":"org-1"}}}`,
	}
}

func observationInput() StaffIdentityInput {
	return StaffIdentityInput{OrganizationID: "org-1", UserID: "TEST-staff-account", Email: "synthetic@example.invalid", GivenName: "Synthetic", FamilyName: "Staff", BindingNonce: "TEST-binding-nonce"}
}

func observationClient(t *testing.T, replies map[string]string, visit func(*http.Request)) *Client {
	t.Helper()
	var mu sync.Mutex
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer TEST-source-token" || r.Header.Get("x-zitadel-orgid") != "org-1" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" {
			t.Error("observation was not an exact scoped read")
		}
		mu.Lock()
		if visit != nil {
			visit(r)
		}
		body, ok := replies[r.URL.Path]
		mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	client := New(server.URL, StaticToken("TEST-source-token"), "platform-1", nil)
	client.http = server.Client()
	return client
}

func TestObserveStaffIdentityRejectsMismatchedOrAmbiguousEvidence(t *testing.T) {
	userPath := "/v2/users/TEST-staff-account"
	metadataPath := "/management/v1/users/TEST-staff-account/metadata/onboarding_staff_command"
	for _, tc := range []struct{ name, path, from, to string }{
		{"wrong account", userPath, `"userId":"TEST-staff-account"`, `"userId":"other"`},
		{"wrong owner", userPath, `"resourceOwner":"org-1"`, `"resourceOwner":"other"`},
		{"changed contact", userPath, "synthetic@example.invalid", "other@example.invalid"},
		{"machine", userPath, `"human":`, `"machine":`},
		{"ambiguous type", userPath, `"human":`, `"machine":{},"human":`},
		{"unknown state", userPath, "USER_STATE_ACTIVE", "USER_STATE_UNSPECIFIED"},
		{"zero user revision", userPath, `"sequence":"101"`, `"sequence":"0"`},
		{"noncanonical user revision", userPath, `"sequence":"101"`, `"sequence":"0101"`},
		{"duplicate state", userPath, `"state":"USER_STATE_ACTIVE"`, `"state":"USER_STATE_LOCKED","state":"USER_STATE_ACTIVE"`},
		{"state alias", userPath, `"state":"USER_STATE_ACTIVE"`, `"State":"USER_STATE_LOCKED","state":"USER_STATE_ACTIVE"`},
		{"missing creation time", userPath, "2026-10-30T10:00:00.123456789Z", ""},
		{"wrong metadata key", metadataPath, "onboarding_staff_command", "other"},
		{"wrong metadata owner", metadataPath, `"resourceOwner":"org-1"`, `"resourceOwner":"other"`},
		{"invalid nonce encoding", metadataPath, base64.StdEncoding.EncodeToString([]byte("TEST-binding-nonce")), "!!!"},
		{"wrong nonce", metadataPath, base64.StdEncoding.EncodeToString([]byte("TEST-binding-nonce")), base64.StdEncoding.EncodeToString([]byte("TEST-unrelated-nonce"))},
		{"zero metadata revision", metadataPath, `"sequence":"102"`, `"sequence":"0"`},
		{"missing metadata", metadataPath, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := staffObservationReplies()
			if tc.name == "missing metadata" {
				delete(replies, tc.path)
			} else {
				replies[tc.path] = strings.Replace(replies[tc.path], tc.from, tc.to, 1)
			}
			client := observationClient(t, replies, nil)
			got, err := client.ObserveStaffIdentity(context.Background(), observationInput(), time.Time{})
			if !errors.Is(err, ErrIdentityUnavailable) || got.UserID != "" || got.EmailVerified {
				t.Fatal("uncertain provider facts established a current identity")
			}
		})
	}
}

func TestObserveStaffIdentityDoesNotPromoteUnverifiedOrInactiveAccounts(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"USER_STATE_ACTIVE", "active"}, {"USER_STATE_INACTIVE", "inactive"},
		{"USER_STATE_LOCKED", "locked"}, {"USER_STATE_INITIAL", "initial"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			replies := staffObservationReplies()
			path := "/v2/users/TEST-staff-account"
			replies[path] = strings.ReplaceAll(strings.ReplaceAll(replies[path], "USER_STATE_ACTIVE", tc.source), `"isVerified":true`, `"isVerified":false`)
			got, err := observationClient(t, replies, nil).ObserveStaffIdentity(context.Background(), observationInput(), time.Time{})
			if err != nil || got.State != tc.want || got.EmailVerified {
				t.Fatalf("ineligible account was promoted: state=%q verified=%t err=%v", got.State, got.EmailVerified, err)
			}
		})
	}
}

func TestObserveStaffIdentityRequiresStableSourceAndRetainedCreationTime(t *testing.T) {
	for _, kind := range []string{"version changed", "state changed", "creation changed", "retained creation mismatch"} {
		t.Run(kind, func(t *testing.T) {
			replies := staffObservationReplies()
			var reads int
			client := observationClient(t, replies, func(r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/v2/users/") {
					reads++
					if reads == 2 {
						switch kind {
						case "version changed":
							replies[r.URL.Path] = strings.ReplaceAll(replies[r.URL.Path], `"sequence":"101"`, `"sequence":"103"`)
						case "state changed":
							replies[r.URL.Path] = strings.ReplaceAll(replies[r.URL.Path], "USER_STATE_ACTIVE", "USER_STATE_LOCKED")
						case "creation changed":
							replies[r.URL.Path] = strings.ReplaceAll(replies[r.URL.Path], "123456789Z", "123456788Z")
						}
					}
				}
			})
			created := time.Date(2026, 10, 30, 10, 0, 0, 123456789, time.UTC)
			if kind == "retained creation mismatch" {
				created = created.Add(time.Nanosecond)
			}
			got, err := client.ObserveStaffIdentity(context.Background(), observationInput(), created)
			if !errors.Is(err, ErrIdentityUnavailable) || got.UserID != "" || got.EmailVerified {
				t.Fatal("changed source or reincarnated account established eligibility")
			}
		})
	}
}

func TestObserveStaffIdentityRejectsRedirectsAndHonorsCancellation(t *testing.T) {
	var redirected atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirected" {
			redirected.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/redirected", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	client := New(server.URL, StaticToken("TEST-source-token"), "platform-1", nil)
	client.http = server.Client()
	got, err := client.ObserveStaffIdentity(context.Background(), observationInput(), time.Time{})
	if !errors.Is(err, ErrIdentityUnavailable) || got.UserID != "" || redirected.Load() != 0 {
		t.Fatal("observation followed a redirect or accepted incomplete proof")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err = client.ObserveStaffIdentity(ctx, observationInput(), time.Time{})
	if !errors.Is(err, ErrIdentityUnavailable) || got.UserID != "" {
		t.Fatal("cancelled observation supplied current proof")
	}
}

func TestObserveStaffIdentityTreatsMissingAndDeletedAccountsAsUnavailableForAccess(t *testing.T) {
	for _, deleted := range []bool{false, true} {
		replies := staffObservationReplies()
		path := "/v2/users/TEST-staff-account"
		if deleted {
			replies[path] = strings.ReplaceAll(replies[path], "USER_STATE_ACTIVE", "USER_STATE_DELETED")
		} else {
			delete(replies, path)
		}
		got, err := observationClient(t, replies, nil).ObserveStaffIdentity(context.Background(), observationInput(), time.Time{})
		if !errors.Is(err, ErrIdentityMissing) || got.UserID != "" || got.EmailVerified {
			t.Fatal("missing or deleted account established current identity proof")
		}
	}
}

func TestObserveStaffIdentityBindsCurrentHumanAndMetadataWithoutDisclosingContact(t *testing.T) {
	var reads, metadataReads atomic.Int32
	client := observationClient(t, staffObservationReplies(), func(r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v2/users/") {
			reads.Add(1)
		} else {
			metadataReads.Add(1)
		}
	})
	created := time.Date(2026, 10, 30, 10, 0, 0, 123456789, time.UTC)
	before := time.Now().UTC()
	got, err := client.ObserveStaffIdentity(context.Background(), observationInput(), created)
	if err != nil || got.UserID != "TEST-staff-account" || got.OrganizationID != "org-1" || got.State != "active" || !got.EmailVerified || !got.CreatedAt.Equal(created) || got.ObservedAt.Before(before) || got.UserRevision != "101" || got.MetadataRevision != "102" || reads.Load() != 2 || metadataReads.Load() != 1 {
		t.Fatalf("invalid current observation: reads=%d metadata=%d err=%v", reads.Load(), metadataReads.Load(), err)
	}
	raw, err := json.Marshal(got)
	if err != nil || strings.Contains(string(raw), "example.invalid") || strings.Contains(string(raw), "TEST-binding-nonce") || strings.Contains(string(raw), "TEST-source-token") {
		t.Fatal("observation disclosed private source evidence")
	}
}
