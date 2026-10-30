package zitadel

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func staffObservationEvent(sequence, kind, key string) map[string]any {
	return map[string]any{"sequence": sequence, "type": map[string]string{"type": kind}, "aggregate": map[string]any{"id": "TEST-staff-account", "resourceOwner": "org-1", "type": map[string]string{"type": "user"}}, "payload": map[string]string{"key": key}}
}

func TestObserveStaffIdentityChecksCommittedMutationScopeAndRevisions(t *testing.T) {
	for _, tc := range []struct {
		name, kind, key string
		allow           bool
	}{
		{"unreflected lock", "user.locked", "", false},
		{"unreflected email", "user.human.email.changed", "", false},
		{"unreflected verification", "user.human.email.verified", "", false},
		{"nonce replacement", "user.metadata.set", "onboarding_staff_command", false},
		{"nonce removal", "user.metadata.removed", "onboarding_staff_command", false},
		{"all metadata removed", "user.metadata.removed.all", "", false},
		{"missing metadata key", "user.metadata.set", "", false},
		{"unrelated metadata", "user.metadata.set", "unrelated_application_key", true},
		{"unrelated removal", "user.metadata.removed", "unrelated_application_key", true},
		{"unreflected initialization", "user.human.initialization.code.added", "", false},
		{"initialization completion without row revision change", "user.human.initialization.check.succeeded", "", true},
		{"unrequested event", "user.password.changed", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := staffObservationReplies()
			raw, err := json.Marshal(map[string]any{"events": []any{staffObservationEvent("103", tc.kind, tc.key)}})
			if err != nil {
				t.Fatal(err)
			}
			replies["/admin/v1/events/_search"] = string(raw)
			got, err := observationClient(t, replies, nil).ObserveStaffIdentity(t.Context(), observationInput(), time.Time{})
			if tc.allow {
				if err != nil || got.State != "active" || !got.EmailVerified {
					t.Fatalf("current proof denied for unrelated/reflected mutation: %v", err)
				}
			} else if !errors.Is(err, ErrIdentityUnavailable) || got.UserID != "" || got.EmailVerified {
				t.Fatal("unreflected mutation established current identity")
			}
		})
	}
}

func TestObserveStaffIdentityRejectsForeignMalformedOrSaturatedEventProof(t *testing.T) {
	for _, kind := range []string{"null", "wrong user", "wrong org", "wrong aggregate", "zero revision", "leading zero", "overflow", "old revision", "duplicate", "descending", "saturated", "duplicate json"} {
		t.Run(kind, func(t *testing.T) {
			event := staffObservationEvent("103", "user.metadata.set", "unrelated_application_key")
			events := []any{event}
			aggregate := event["aggregate"].(map[string]any)
			switch kind {
			case "wrong user":
				aggregate["id"] = "other"
			case "wrong org":
				aggregate["resourceOwner"] = "other"
			case "wrong aggregate":
				aggregate["type"] = map[string]string{"type": "org"}
			case "zero revision":
				event["sequence"] = "0"
			case "leading zero":
				event["sequence"] = "0103"
			case "overflow":
				event["sequence"] = "18446744073709551616"
			case "old revision":
				event["sequence"] = "101"
			case "duplicate":
				events = append(events, event)
			case "descending":
				events = append(events, staffObservationEvent("102", "user.metadata.set", "unrelated_application_key"))
			case "saturated":
				for i := 1; i < 32; i++ {
					events = append(events, staffObservationEvent(strconv.Itoa(103+i), "user.metadata.set", "unrelated_application_key"))
				}
			}
			raw, err := json.Marshal(map[string]any{"events": events})
			if err != nil {
				t.Fatal(err)
			}
			if kind == "null" {
				raw = []byte("null")
			}
			if kind == "duplicate json" {
				raw = []byte(`{"events":[{}],"events":[]}`)
			}
			replies := staffObservationReplies()
			replies["/admin/v1/events/_search"] = string(raw)
			got, err := observationClient(t, replies, nil).ObserveStaffIdentity(t.Context(), observationInput(), time.Time{})
			if !errors.Is(err, ErrIdentityUnavailable) || got.UserID != "" || got.EmailVerified {
				t.Fatal("foreign/malformed/truncated event proof established current identity")
			}
		})
	}
}

func TestObserveStaffIdentityBoundsAndPinsEventStoreRequest(t *testing.T) {
	var eventReads int
	client := observationClient(t, staffObservationReplies(), func(r *http.Request) {
		if r.URL.Path != "/admin/v1/events/_search" {
			return
		}
		eventReads++
		var body struct {
			AggregateID, ResourceOwner, Sequence string
			AggregateTypes, EventTypes           []string
			Limit                                int
			Asc                                  bool
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.AggregateID != "TEST-staff-account" || body.ResourceOwner != "org-1" || len(body.AggregateTypes) != 1 || body.AggregateTypes[0] != "user" {
			t.Error("event search was not exact and bounded")
		}
		if body.Limit == 1 {
			if body.Sequence != "0" || body.Asc || (len(body.EventTypes) != 9 && len(body.EventTypes) != 2) {
				t.Error("latest field search was not exact and bounded")
			}
		} else if body.Limit != 32 || body.Sequence != "101" || !body.Asc || len(body.EventTypes) != 16 {
			t.Error("mutation guard was not exact and bounded")
		}
		seen := map[string]bool{}
		for _, kind := range body.EventTypes {
			if seen[kind] || strings.Contains(kind, "password") || strings.Contains(kind, "login") {
				t.Error("event search requested unnecessary/duplicate sensitive event")
			}
			seen[kind] = true
		}
		if body.Limit == 32 {
			for _, kind := range []string{"user.deactivated", "user.removed", "user.human.email.changed", "user.human.email.verified", "user.metadata.set", "user.metadata.removed", "user.metadata.removed.all", "user.human.initialization.code.added", "user.human.initialization.check.succeeded"} {
				if !seen[kind] {
					t.Error("event search omitted a relevant mutation")
				}
			}
		}
	})
	got, err := client.ObserveStaffIdentity(t.Context(), observationInput(), time.Time{})
	if err != nil || got.State != "active" || eventReads != 4 {
		t.Fatalf("bounded current observation failed: events=%d err=%v", eventReads, err)
	}
}

func TestObserveStaffIdentityDeniesUnavailableOrRedirectedEventStore(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusServiceUnavailable, http.StatusTemporaryRedirect} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			var followed atomic.Int32
			replies := staffObservationReplies()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/redirected-events" {
					followed.Add(1)
					_, _ = w.Write([]byte(`{"events":[]}`))
					return
				}
				if r.URL.Path == "/admin/v1/events/_search" {
					w.Header().Set("Location", "/redirected-events")
					w.WriteHeader(status)
					return
				}
				_, _ = w.Write([]byte(replies[r.URL.Path]))
			}))
			t.Cleanup(server.Close)
			client := New(server.URL, StaticToken("TEST-source-token"), "platform-1", nil)
			client.http = server.Client()
			got, err := client.ObserveStaffIdentity(t.Context(), observationInput(), time.Time{})
			if !errors.Is(err, ErrIdentityUnavailable) || got.UserID != "" || got.EmailVerified || followed.Load() != 0 {
				t.Fatal("unavailable or redirected event store established current identity")
			}
		})
	}
}

func TestObserveStaffIdentityRejectsSkippedDeactivationDespiteLaterProfileRevision(t *testing.T) {
	replies := staffObservationReplies()
	raw, err := json.Marshal(map[string]any{"events": []any{staffObservationEvent("99", "user.deactivated", "")}})
	if err != nil {
		t.Fatal(err)
	}
	replies["/admin/v1/events/_search#state"] = string(raw)
	got, err := observationClient(t, replies, nil).ObserveStaffIdentity(t.Context(), observationInput(), time.Time{})
	if !errors.Is(err, ErrIdentityUnavailable) || got.UserID != "" || got.EmailVerified {
		t.Fatal("skipped deactivation was hidden by a later unrelated profile revision")
	}
}

func TestObserveStaffIdentityRejectsSkippedContactChangesDespiteLaterProfileRevision(t *testing.T) {
	for _, tc := range []struct{ name, email, verified string }{
		{"changed contact", "other@example.invalid", "100"},
		{"verification predates contact change", "synthetic@example.invalid", "98"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			replies := staffObservationReplies()
			event := staffObservationEvent("99", "user.human.email.changed", "")
			event["payload"] = map[string]string{"email": tc.email}
			raw, err := json.Marshal(map[string]any{"events": []any{event}})
			if err != nil {
				t.Fatal(err)
			}
			replies["/admin/v1/events/_search#email-change"] = string(raw)
			raw, err = json.Marshal(map[string]any{"events": []any{staffObservationEvent(tc.verified, "user.human.email.verified", "")}})
			if err != nil {
				t.Fatal(err)
			}
			replies["/admin/v1/events/_search#email-verified"] = string(raw)
			got, err := observationClient(t, replies, nil).ObserveStaffIdentity(t.Context(), observationInput(), time.Time{})
			if !errors.Is(err, ErrIdentityUnavailable) || got.UserID != "" || got.EmailVerified {
				t.Fatal("skipped contact mutation borrowed a later profile revision")
			}
		})
	}
}

func TestObserveStaffIdentityRejectsForeignOrAmbiguousLatestFieldProof(t *testing.T) {
	for _, phase := range []string{"state", "email-change", "email-verified"} {
		for _, kind := range []string{"null", "foreign", "wrong event", "multiple", "zero"} {
			t.Run(phase+"/"+kind, func(t *testing.T) {
				eventType := map[string]string{"state": "user.reactivated", "email-change": "user.human.email.changed", "email-verified": "user.human.email.verified"}[phase]
				event := staffObservationEvent("100", eventType, "")
				event["payload"] = map[string]string{"email": "synthetic@example.invalid"}
				if kind == "foreign" {
					event["aggregate"].(map[string]any)["resourceOwner"] = "other"
				}
				if kind == "wrong event" {
					event["type"] = map[string]string{"type": "user.password.changed"}
				}
				if kind == "zero" {
					event["sequence"] = "0"
				}
				events := []any{event}
				if kind == "multiple" {
					events = append(events, event)
				}
				raw, err := json.Marshal(map[string]any{"events": events})
				if err != nil {
					t.Fatal(err)
				}
				if kind == "null" {
					raw = []byte("null")
				}
				replies := staffObservationReplies()
				replies["/admin/v1/events/_search#"+phase] = string(raw)
				got, err := observationClient(t, replies, nil).ObserveStaffIdentity(t.Context(), observationInput(), time.Time{})
				if !errors.Is(err, ErrIdentityUnavailable) || got.UserID != "" || got.EmailVerified {
					t.Fatal("foreign/ambiguous latest-field event established identity")
				}
			})
		}
	}
}
