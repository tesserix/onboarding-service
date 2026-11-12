package zitadel

import (
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
	"sync/atomic"
	"testing"
	"time"
)

type operatorFixture struct {
	user   string
	grants string
	events map[string][]map[string]any
	visit  func(*http.Request)
}

func authorityEvent(seq, id, kind, event string, payload map[string]any) map[string]any {
	return map[string]any{"sequence": seq, "aggregate": map[string]any{"id": id, "resourceOwner": "platform-1", "type": map[string]string{"type": kind}}, "type": map[string]string{"type": event}, "payload": payload}
}

func newOperatorFixture() *operatorFixture {
	return &operatorFixture{
		user:   `{"user":{"userId":"TEST-operator","state":"USER_STATE_ACTIVE","details":{"sequence":"21","resourceOwner":"platform-1","creationDate":"2026-10-30T10:00:00Z"},"human":{"email":{"email":"PRIVATE_OPERATOR@example.invalid","isVerified":true}}}}`,
		grants: `{"result":[{"id":"grant-1","userId":"TEST-operator","projectId":"console-1","projectGrantId":"","details":{"resourceOwner":"platform-1"}}]}`,
		events: map[string][]map[string]any{
			"platform-1":    {authorityEvent("10", "platform-1", "org", "org.added", nil)},
			"console-1":     {authorityEvent("11", "console-1", "project", "project.added", nil), authorityEvent("12", "console-1", "project", "project.role.added", map[string]any{"key": "platform"}), authorityEvent("13", "console-1", "project", "project.role.added", map[string]any{"key": "rotate-credentials"})},
			"grant-1":       {authorityEvent("20", "grant-1", "usergrant", "user.grant.added", map[string]any{"userId": "TEST-operator", "projectId": "console-1", "roleKeys": []string{"platform", "rotate-credentials"}})},
			"TEST-operator": {authorityEvent("18", "TEST-operator", "user", "user.human.email.verified", nil), authorityEvent("19", "TEST-operator", "user", "user.reactivated", nil)},
		},
	}
}

func (f *operatorFixture) client(t *testing.T) *Client {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer TEST-operator-source" || r.Header.Get("x-zitadel-orgid") != "platform-1" || r.Header.Get("Cookie") != "" || r.URL.RawQuery != "" {
			t.Error("operator proof crossed an unscoped credential boundary")
		}
		if f.visit != nil {
			f.visit(r)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/users/TEST-operator":
			if r.Method != http.MethodGet {
				t.Error("user proof is not read-only")
			}
			_, _ = w.Write([]byte(f.user))
		case "/management/v1/users/grants/_search":
			if r.Method != http.MethodPost {
				t.Error("grant search method")
			}
			var q struct {
				Query   struct{ Limit string }
				Queries []map[string]map[string]string
			}
			if json.NewDecoder(r.Body).Decode(&q) != nil || q.Query.Limit != "2" || len(q.Queries) != 2 || q.Queries[0]["userIdQuery"]["userId"] != "TEST-operator" || q.Queries[1]["projectIdQuery"]["projectId"] != "console-1" {
				t.Error("unbounded or unscoped grant discovery")
			}
			_, _ = w.Write([]byte(f.grants))
		case "/admin/v1/events/_search":
			var q struct {
				AggregateID    string
				AggregateTypes []string
				ResourceOwner  string
				EventTypes     []string
				Sequence       string
				Asc            bool
				Limit          int
			}
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&q) != nil || q.ResourceOwner != "platform-1" || len(q.AggregateTypes) != 1 || q.Asc || q.Limit < 1 || q.Limit > 64 {
				t.Error("unbounded/unscoped operator event query")
			}
			rows := []map[string]any{}
			for _, e := range f.events[q.AggregateID] {
				typ := e["type"].(map[string]string)["type"]
				seq, _ := strconv.ParseUint(e["sequence"].(string), 10, 64)
				upper, _ := strconv.ParseUint(q.Sequence, 10, 64)
				if slices.Contains(q.EventTypes, typ) && (upper == 0 || seq < upper) {
					rows = append(rows, e)
				}
			}
			sort.Slice(rows, func(i, j int) bool {
				a, _ := strconv.ParseUint(rows[i]["sequence"].(string), 10, 64)
				b, _ := strconv.ParseUint(rows[j]["sequence"].(string), 10, 64)
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
	c := New(server.URL, StaticToken("TEST-operator-source"), "platform-1", nil)
	c.http = server.Client()
	return c
}

func TestStaffRecoveryOperatorRequiresCurrentHumanConsoleAuthority(t *testing.T) {
	f := newOperatorFixture()
	c := f.client(t)
	started := time.Now()
	proof, err := c.VerifyStaffRecoveryOperator(t.Context(), "TEST-operator", "console-1", true)
	if err != nil {
		t.Fatal(err)
	}
	if proof.Subject != "TEST-operator" || proof.OrganizationID != "platform-1" || proof.ProjectID != "console-1" || proof.GrantID != "grant-1" || !proof.Write || proof.VerifiedAt.Before(started) || proof.VerifiedAt.After(time.Now()) {
		t.Fatal("operator authority lost exact current binding")
	}
}

func TestStaffRecoveryOperatorDeniesWithdrawnOrForeignAuthority(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*operatorFixture)
	}{
		{"inactive human", func(f *operatorFixture) {
			f.user = strings.ReplaceAll(f.user, "USER_STATE_ACTIVE", "USER_STATE_INACTIVE")
		}},
		{"unverified contact", func(f *operatorFixture) {
			f.user = strings.ReplaceAll(f.user, `"isVerified":true`, `"isVerified":false`)
		}},
		{"missing grant", func(f *operatorFixture) { f.grants = `{"result":[]}` }},
		{"foreign grant subject", func(f *operatorFixture) { f.grants = strings.ReplaceAll(f.grants, "TEST-operator", "other-human") }},
		{"foreign grant project", func(f *operatorFixture) { f.grants = strings.ReplaceAll(f.grants, "console-1", "other-console") }},
		{"foreign grant owner", func(f *operatorFixture) { f.grants = strings.ReplaceAll(f.grants, "platform-1", "other-org") }},
		{"delegated grant", func(f *operatorFixture) {
			f.grants = strings.ReplaceAll(f.grants, `"projectGrantId":""`, `"projectGrantId":"delegated"`)
		}},
		{"grant removed", func(f *operatorFixture) {
			f.events["grant-1"] = append(f.events["grant-1"], authorityEvent("22", "grant-1", "usergrant", "user.grant.removed", nil))
		}},
		{"grant cascade removed", func(f *operatorFixture) {
			f.events["grant-1"] = append(f.events["grant-1"], authorityEvent("22", "grant-1", "usergrant", "user.grant.cascade.removed", nil))
		}},
		{"grant deactivated", func(f *operatorFixture) {
			f.events["grant-1"] = append(f.events["grant-1"], authorityEvent("22", "grant-1", "usergrant", "user.grant.deactivated", nil))
		}},
		{"write role withdrawn", func(f *operatorFixture) {
			f.events["grant-1"] = append(f.events["grant-1"], authorityEvent("22", "grant-1", "usergrant", "user.grant.changed", map[string]any{"userId": "TEST-operator", "roleKeys": []string{"platform"}}))
		}},
		{"platform role withdrawn", func(f *operatorFixture) {
			f.events["grant-1"] = append(f.events["grant-1"], authorityEvent("22", "grant-1", "usergrant", "user.grant.cascade.changed", map[string]any{"roleKeys": []string{"rotate-credentials"}}))
		}},
		{"write definition removed", func(f *operatorFixture) {
			f.events["console-1"] = append(f.events["console-1"], authorityEvent("22", "console-1", "project", "project.role.removed", map[string]any{"key": "rotate-credentials"}))
		}},
		{"platform definition removed", func(f *operatorFixture) {
			f.events["console-1"] = append(f.events["console-1"], authorityEvent("22", "console-1", "project", "project.role.removed", map[string]any{"key": "platform"}))
		}},
		{"project deactivated", func(f *operatorFixture) {
			f.events["console-1"] = append(f.events["console-1"], authorityEvent("22", "console-1", "project", "project.deactivated", nil))
		}},
		{"organization removed", func(f *operatorFixture) {
			f.events["platform-1"] = append(f.events["platform-1"], authorityEvent("22", "platform-1", "org", "org.removed", nil))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOperatorFixture()
			tc.change(f)
			proof, err := f.client(t).VerifyStaffRecoveryOperator(t.Context(), "TEST-operator", "console-1", true)
			if !errors.Is(err, ErrStaffOperatorDenied) || proof != (StaffRecoveryOperatorProof{}) {
				t.Fatalf("withdrawn authority released proof: %v", err)
			}
		})
	}
}

func TestStaffRecoveryOperatorFailsClosedOnUncertainProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*operatorFixture)
	}{
		{"stale active user", func(f *operatorFixture) {
			f.events["TEST-operator"] = append(f.events["TEST-operator"], authorityEvent("22", "TEST-operator", "user", "user.deactivated", nil))
		}},
		{"stale contact", func(f *operatorFixture) {
			f.events["TEST-operator"] = append(f.events["TEST-operator"], authorityEvent("22", "TEST-operator", "user", "user.human.email.changed", map[string]any{"email": "different@example.invalid"}))
		}},
		{"missing project history", func(f *operatorFixture) { f.events["console-1"] = nil }},
		{"missing grant history", func(f *operatorFixture) { f.events["grant-1"] = nil }},
		{"wrong event subject", func(f *operatorFixture) {
			f.events["grant-1"][0] = authorityEvent("20", "other-grant", "usergrant", "user.grant.added", nil)
		}},
		{"wrong event binding", func(f *operatorFixture) {
			f.events["grant-1"][0] = authorityEvent("20", "grant-1", "usergrant", "user.grant.added", map[string]any{"userId": "other-human", "projectId": "console-1", "roleKeys": []string{"platform", "rotate-credentials"}})
		}},
		{"duplicate JSON field", func(f *operatorFixture) { f.grants = `{"result":[],"result":[]}` }},
		{"null grant response", func(f *operatorFixture) { f.grants = `null` }},
		{"machine instead of human", func(f *operatorFixture) {
			f.user = strings.Replace(f.user, `"human":`, `"machine":{"name":"synthetic"},"human":`, 1)
		}},
		{"saturated history", func(f *operatorFixture) {
			for i := 0; i < 64; i++ {
				f.events["console-1"] = append(f.events["console-1"], authorityEvent(strconv.Itoa(30+i), "console-1", "project", "project.role.changed", map[string]any{"key": "unrelated-role"}))
			}
		}},
		{"source churn", func(f *operatorFixture) {
			count := 0
			f.visit = func(r *http.Request) {
				if r.URL.Path == "/admin/v1/events/_search" {
					count++
					if count == 5 {
						f.events["grant-1"] = append(f.events["grant-1"], authorityEvent("22", "grant-1", "usergrant", "user.grant.deactivated", nil))
					}
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newOperatorFixture()
			tc.change(f)
			proof, err := f.client(t).VerifyStaffRecoveryOperator(t.Context(), "TEST-operator", "console-1", true)
			if !errors.Is(err, ErrIdentityUnavailable) || proof != (StaffRecoveryOperatorProof{}) {
				t.Fatalf("uncertain authority released proof: %v", err)
			}
		})
	}
}

func TestStaffRecoveryOperatorReadAuthorityDoesNotRequireWriteRole(t *testing.T) {
	f := newOperatorFixture()
	f.events["grant-1"] = append(f.events["grant-1"], authorityEvent("22", "grant-1", "usergrant", "user.grant.changed", map[string]any{"userId": "TEST-operator", "roleKeys": []string{"platform"}}))
	f.events["console-1"] = append(f.events["console-1"], authorityEvent("23", "console-1", "project", "project.role.removed", map[string]any{"key": "rotate-credentials"}))
	proof, err := f.client(t).VerifyStaffRecoveryOperator(t.Context(), "TEST-operator", "console-1", false)
	if err != nil || proof.Subject != "TEST-operator" || proof.Write {
		t.Fatalf("current read-only operator denied: %v", err)
	}
}

func TestStaffRecoveryOperatorDoesNotSendAmbientCookies(t *testing.T) {
	f := newOperatorFixture()
	c := f.client(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(c.base)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "ambient-session", Value: "PRIVATE-ambient", Path: "/"}})
	c.http.Jar = jar
	proof, err := c.VerifyStaffRecoveryOperator(t.Context(), "TEST-operator", "console-1", true)
	if err != nil || proof.Subject != "TEST-operator" {
		t.Fatalf("cookie-free proof unavailable: %v", err)
	}
	if got := fmt.Sprint(proof); got != "[redacted staff recovery authority]" {
		t.Fatal("operator proof formatting discloses source identity")
	}
}

func TestStaffRecoveryOperatorDoesNotFollowRedirectsOrReleaseFailedSourceProof(t *testing.T) {
	var visits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { visits.Add(1); w.WriteHeader(http.StatusOK) }))
	t.Cleanup(target.Close)
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
			}))
			t.Cleanup(source.Close)
			c := New(source.URL, StaticToken("TEST-source"), "platform-1", nil)
			proof, err := c.VerifyStaffRecoveryOperator(t.Context(), "TEST-operator", "console-1", true)
			if !errors.Is(err, ErrIdentityUnavailable) || proof != (StaffRecoveryOperatorProof{}) || visits.Load() != 0 {
				t.Fatalf("source failure released or redirected proof: %v", err)
			}
		})
	}
}

func TestStaffRecoveryOperatorRejectsInvalidScopeBeforeSourceIO(t *testing.T) {
	f := newOperatorFixture()
	visits := 0
	f.visit = func(*http.Request) { visits++ }
	c := f.client(t)
	for _, scope := range []struct{ subject, project string }{{"", "console-1"}, {"TEST-operator", ""}, {"../private", "console-1"}, {"TEST-operator", "console/other"}} {
		proof, err := c.VerifyStaffRecoveryOperator(t.Context(), scope.subject, scope.project, true)
		if !errors.Is(err, ErrStaffOperatorDenied) || proof != (StaffRecoveryOperatorProof{}) || visits != 0 {
			t.Fatal("invalid scope reached provider or released proof")
		}
	}
	c.tokens = StaticToken("")
	proof, err := c.VerifyStaffRecoveryOperator(t.Context(), "TEST-operator", "console-1", true)
	if !errors.Is(err, ErrIdentityUnavailable) || proof != (StaffRecoveryOperatorProof{}) || visits != 0 {
		t.Fatal("missing credential reached provider or released proof")
	}
}
