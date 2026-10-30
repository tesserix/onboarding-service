package zitadel

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recoverySubjectFixture struct {
	mu       sync.Mutex
	status   int
	user     string
	metadata string
	events   []map[string]any
	visit    func(*http.Request)
}

func (f *recoverySubjectFixture) client(t *testing.T) *Client {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer TEST-recovery-source" || r.Header.Get("x-zitadel-orgid") != "org-1" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" {
			t.Error("recovery subject crossed credential/scope boundary")
		}
		if f.visit != nil {
			f.visit(r)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/users/TEST-staff-account":
			if r.Method != http.MethodGet {
				t.Error("recovery subject mutated source")
			}
			w.WriteHeader(f.status)
			_, _ = w.Write([]byte(f.user))
		case "/management/v1/users/TEST-staff-account/metadata/onboarding_staff_command":
			if r.Method != http.MethodGet {
				t.Error("recovery metadata mutated source")
			}
			_, _ = w.Write([]byte(f.metadata))
		case "/admin/v1/events/_search":
			var q struct {
				AggregateID, ResourceOwner, Sequence string
				AggregateTypes, EventTypes           []string
				Asc                                  bool
				Limit                                int
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&q) != nil || q.AggregateID != "TEST-staff-account" || q.ResourceOwner != "org-1" || !slices.Equal(q.AggregateTypes, []string{"user"}) || q.Limit < 1 || q.Limit > 64 {
				t.Error("recovery subject history unbounded/unscoped")
			}
			bound, _ := strconv.ParseUint(q.Sequence, 10, 64)
			rows := []map[string]any{}
			for _, event := range f.events {
				seq, _ := strconv.ParseUint(event["sequence"].(string), 10, 64)
				typ := event["type"].(map[string]string)["type"]
				if (len(q.EventTypes) == 0 || slices.Contains(q.EventTypes, typ)) && (bound == 0 || q.Asc && seq > bound || !q.Asc && seq < bound) {
					rows = append(rows, event)
				}
			}
			sort.Slice(rows, func(i, j int) bool {
				a, _ := strconv.ParseUint(rows[i]["sequence"].(string), 10, 64)
				b, _ := strconv.ParseUint(rows[j]["sequence"].(string), 10, 64)
				if q.Asc {
					return a < b
				}
				return a > b
			})
			if len(rows) > q.Limit {
				rows = rows[:q.Limit]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"events": rows})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	c := New(server.URL, StaticToken("TEST-recovery-source"), "platform-1", nil)
	c.http = server.Client()
	return c
}

func TestStaffRecoverySubjectAbsenceRequiresTwoSourceReadsAndStableEmptyHistory(t *testing.T) {
	gets, heads := 0, 0
	f := &recoverySubjectFixture{status: http.StatusNotFound}
	f.visit = func(r *http.Request) {
		if r.URL.Path == "/v2/users/TEST-staff-account" {
			gets++
		}
		if r.URL.Path == "/admin/v1/events/_search" {
			heads++
		}
	}
	started := time.Now()
	proof, err := f.client(t).InspectStaffRecoverySubject(t.Context(), observationInput(), time.Date(2026, 10, 30, 9, 59, 59, 0, time.UTC))
	if err != nil || proof.UserID != "TEST-staff-account" || proof.OrganizationID != "org-1" || proof.Disposition != "absent" || !proof.CreatedAt.IsZero() || proof.VerifiedAt.Before(started) || proof.VerifiedAt.After(time.Now()) || gets != 2 || heads != 2 {
		t.Fatalf("absence without exact stable source proof: gets=%d heads=%d err=%v", gets, heads, err)
	}
}

func recoverySubjectEvent(sequence, typ string, payload map[string]any) map[string]any {
	return map[string]any{"sequence": sequence, "creationDate": "2026-10-30T10:00:00.123456789Z", "aggregate": map[string]any{"id": "TEST-staff-account", "resourceOwner": "org-1", "type": map[string]string{"type": "user"}}, "type": map[string]string{"type": typ}, "payload": payload}
}

func matchingRecoverySubjectFixture() *recoverySubjectFixture {
	replies := staffObservationReplies()
	return &recoverySubjectFixture{status: http.StatusOK, user: replies["/v2/users/TEST-staff-account"], metadata: replies["/management/v1/users/TEST-staff-account/metadata/onboarding_staff_command"], events: []map[string]any{
		recoverySubjectEvent("90", "user.human.added", map[string]any{"email": "synthetic@example.invalid"}),
		recoverySubjectEvent("100", "user.human.email.verified", nil),
		recoverySubjectEvent("102", "user.metadata.set", map[string]any{"key": "onboarding_staff_command", "value": base64.StdEncoding.EncodeToString([]byte("TEST-binding-nonce"))}),
	}}
}

func TestStaffRecoverySubjectRecognizesMatchingOrphanWithoutCreatingOrVerifyingIt(t *testing.T) {
	f := matchingRecoverySubjectFixture()
	proof, err := f.client(t).InspectStaffRecoverySubject(t.Context(), observationInput(), time.Date(2026, 10, 30, 9, 59, 59, 0, time.UTC))
	created, _ := time.Parse(time.RFC3339Nano, "2026-10-30T10:00:00.123456789Z")
	if err != nil || proof.Disposition != "matching" || proof.UserID != "TEST-staff-account" || proof.OrganizationID != "org-1" || proof.ObjectRevision != "102" || !proof.CreatedAt.Equal(created) {
		t.Fatalf("matching orphan became absent or lost sealed birth: %v", err)
	}
}

func TestStaffRecoverySubjectRemovalRequiresRetainedBirthAndSealedBinding(t *testing.T) {
	f := matchingRecoverySubjectFixture()
	f.status = http.StatusNotFound
	f.user = ""
	f.events = append(f.events, recoverySubjectEvent("103", "user.removed", nil))
	proof, err := f.client(t).InspectStaffRecoverySubject(t.Context(), observationInput(), time.Date(2026, 10, 30, 9, 59, 59, 0, time.UTC))
	created, _ := time.Parse(time.RFC3339Nano, "2026-10-30T10:00:00.123456789Z")
	if err != nil || proof.Disposition != "removed" || proof.UserID != "TEST-staff-account" || proof.OrganizationID != "org-1" || proof.ObjectRevision != "103" || !proof.CreatedAt.Equal(created) {
		t.Fatalf("removed subject lost retained ownership or became never-created: %v", err)
	}
}

func TestStaffRecoverySubjectRejectsStaleOrContradictorySourceFacts(t *testing.T) {
	for _, kind := range []string{"stale not found", "present without history", "foreign owner", "wrong contact", "wrong nonce", "wrong birth contact", "wrong birth time", "machine birth", "tombstone without history", "source forbidden", "source throttled", "source outage", "matching source churn", "absence source churn"} {
		t.Run(kind, func(t *testing.T) {
			f := matchingRecoverySubjectFixture()
			switch kind {
			case "stale not found":
				f.status = http.StatusNotFound
				f.user = ""
			case "present without history":
				f.events = nil
			case "foreign owner":
				f.user = strings.ReplaceAll(f.user, `"resourceOwner":"org-1"`, `"resourceOwner":"org-other"`)
			case "wrong contact":
				f.user = strings.ReplaceAll(f.user, "synthetic@example.invalid", "other@example.invalid")
			case "wrong nonce":
				f.metadata = strings.ReplaceAll(f.metadata, base64.StdEncoding.EncodeToString([]byte("TEST-binding-nonce")), base64.StdEncoding.EncodeToString([]byte("TEST-foreign-binding")))
			case "wrong birth contact":
				f.events[0] = recoverySubjectEvent("90", "user.human.added", map[string]any{"email": "other@example.invalid"})
			case "wrong birth time":
				f.events[0]["creationDate"] = "2025-01-01T00:00:00Z"
			case "machine birth":
				f.events[0] = recoverySubjectEvent("90", "user.machine.added", map[string]any{"email": "synthetic@example.invalid"})
			case "tombstone without history":
				f.events = nil
				f.user = strings.ReplaceAll(f.user, "USER_STATE_ACTIVE", "USER_STATE_DELETED")
			case "source forbidden":
				f.events = nil
				f.status = http.StatusForbidden
			case "source throttled":
				f.events = nil
				f.status = http.StatusTooManyRequests
			case "source outage":
				f.events = nil
				f.status = http.StatusServiceUnavailable
			case "matching source churn":
				gets := 0
				f.visit = func(r *http.Request) {
					if r.URL.Path == "/v2/users/TEST-staff-account" {
						gets++
						if gets == 2 {
							f.events = append(f.events, recoverySubjectEvent("104", "user.password.check.succeeded", nil))
						}
					}
				}
			case "absence source churn":
				f.events = nil
				f.status = http.StatusNotFound
				heads := 0
				f.visit = func(r *http.Request) {
					if r.URL.Path == "/admin/v1/events/_search" {
						heads++
						if heads == 2 {
							f.events = append(f.events, recoverySubjectEvent("90", "user.human.added", map[string]any{"email": "synthetic@example.invalid"}))
						}
					}
				}
			}
			proof, err := f.client(t).InspectStaffRecoverySubject(t.Context(), observationInput(), time.Date(2026, 10, 30, 9, 59, 59, 0, time.UTC))
			if !errors.Is(err, ErrIdentityUnavailable) || proof != (StaffRecoverySubjectProof{}) {
				t.Fatalf("uncertain subject released recovery proof: %v", err)
			}
		})
	}
}

func TestStaffRecoverySubjectRemovalRejectsLostOwnershipAndSaturatedHistory(t *testing.T) {
	for _, kind := range []string{"wrong historical nonce", "missing historical nonce", "removed historical nonce", "cleared historical metadata", "multiple births", "binding before birth", "nonterminal removal", "stale active projection", "deleted projection only", "saturated history", "duplicate history revision"} {
		t.Run(kind, func(t *testing.T) {
			f := matchingRecoverySubjectFixture()
			f.status = http.StatusNotFound
			f.user = ""
			f.events = append(f.events, recoverySubjectEvent("200", "user.removed", nil))
			switch kind {
			case "wrong historical nonce":
				f.events[2] = recoverySubjectEvent("102", "user.metadata.set", map[string]any{"key": "onboarding_staff_command", "value": base64.StdEncoding.EncodeToString([]byte("TEST-foreign-binding"))})
			case "missing historical nonce":
				f.events = append(f.events[:2], f.events[3])
			case "removed historical nonce":
				f.events = append(f.events, recoverySubjectEvent("103", "user.metadata.removed", map[string]any{"key": "onboarding_staff_command"}))
			case "cleared historical metadata":
				f.events = append(f.events, recoverySubjectEvent("103", "user.metadata.removed.all", nil))
			case "multiple births":
				f.events = append(f.events, recoverySubjectEvent("91", "user.human.added", map[string]any{"email": "synthetic@example.invalid"}))
			case "binding before birth":
				f.events[2] = recoverySubjectEvent("89", "user.metadata.set", map[string]any{"key": "onboarding_staff_command", "value": base64.StdEncoding.EncodeToString([]byte("TEST-binding-nonce"))})
			case "nonterminal removal":
				f.events = append(f.events, recoverySubjectEvent("201", "user.metadata.set", map[string]any{"key": "other", "value": "eA=="}))
			case "stale active projection":
				f.status = http.StatusOK
				f.user = staffObservationReplies()["/v2/users/TEST-staff-account"]
			case "deleted projection only":
				f.status = http.StatusOK
				f.user = strings.ReplaceAll(staffObservationReplies()["/v2/users/TEST-staff-account"], "USER_STATE_ACTIVE", "USER_STATE_DELETED")
			case "saturated history":
				for i := 0; i < 64; i++ {
					f.events = append(f.events, recoverySubjectEvent(strconv.Itoa(110+i), "user.metadata.set", map[string]any{"key": "other", "value": "eA=="}))
				}
			case "duplicate history revision":
				f.events = append(f.events, recoverySubjectEvent("90", "user.human.added", map[string]any{"email": "synthetic@example.invalid"}))
			}
			proof, err := f.client(t).InspectStaffRecoverySubject(t.Context(), observationInput(), time.Date(2026, 10, 30, 9, 59, 59, 0, time.UTC))
			if !errors.Is(err, ErrIdentityUnavailable) || proof != (StaffRecoverySubjectProof{}) {
				t.Fatalf("lost historical ownership released removal proof: %v", err)
			}
		})
	}
}

func TestStaffRecoverySubjectRejectsInvalidInputBeforeSourceIO(t *testing.T) {
	f := &recoverySubjectFixture{status: http.StatusNotFound}
	var visits atomic.Int32
	f.visit = func(*http.Request) { visits.Add(1) }
	c := f.client(t)
	for _, kind := range []string{"account", "nonce", "zero preparation", "future preparation"} {
		in := observationInput()
		at := time.Date(2026, 10, 30, 9, 59, 59, 0, time.UTC)
		switch kind {
		case "account":
			in.UserID = "../other"
		case "nonce":
			in.BindingNonce = "short"
		case "zero preparation":
			at = time.Time{}
		case "future preparation":
			at = time.Now().Add(time.Hour)
		}
		got, err := c.InspectStaffRecoverySubject(t.Context(), in, at)
		if !errors.Is(err, ErrStaffIdentityInvalid) || got != (StaffRecoverySubjectProof{}) || visits.Load() != 0 {
			t.Fatal("invalid frozen input reached source")
		}
	}
}

func TestStaffRecoverySubjectDoesNotSendAmbientCookies(t *testing.T) {
	f := &recoverySubjectFixture{status: http.StatusNotFound}
	c := f.client(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(c.base)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "ambient", Value: "PRIVATE-session", Path: "/"}})
	c.http.Jar = jar
	proof, err := c.InspectStaffRecoverySubject(t.Context(), observationInput(), time.Date(2026, 10, 30, 9, 59, 59, 0, time.UTC))
	if err != nil || proof.Disposition != "absent" || fmt.Sprint(proof) != "[redacted staff recovery subject proof]" {
		t.Fatalf("cookie-free or redacted proof failed: %v", err)
	}
}

func TestStaffRecoverySubjectNeverFollowsSourceRedirects(t *testing.T) {
	var visits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { visits.Add(1); w.WriteHeader(http.StatusOK) }))
	t.Cleanup(target.Close)
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/v1/events/_search" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"events":[]}`))
			return
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(source.Close)
	c := New(source.URL, StaticToken("TEST-recovery-source"), "platform-1", nil)
	c.http = source.Client()
	proof, err := c.InspectStaffRecoverySubject(t.Context(), observationInput(), time.Date(2026, 10, 30, 9, 59, 59, 0, time.UTC))
	if !errors.Is(err, ErrIdentityUnavailable) || proof != (StaffRecoverySubjectProof{}) || visits.Load() != 0 {
		t.Fatal("recovery subject proof followed redirect or released facts")
	}
}
