package zitadel

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRetainedNativeOrganizationKeepsTheExplicitTenantOnSelfReads(t *testing.T) {
	var calls atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v2/users/retained-user" {
			t.Error("native read selected another subject")
		}
		_, _ = fmt.Fprint(w, `{"user":{"userId":"retained-user","state":"USER_STATE_ACTIVE","details":{"resourceOwner":"123456789"},"human":{"email":{"email":"retained@example.test","isVerified":true},"profile":{"displayName":"TEST Retained User"}}}}`)
	}))
	defer native.Close()
	client := New(native.URL, StaticToken("synthetic"), "platform", nil)
	for _, purpose := range []string{"status", "profile"} {
		proof, err := client.IdentityProjection(t.Context(), membershipTestTenant, "123456789", "retained-user", purpose)
		if err != nil || proof.Validate() != nil || proof.TenantID != membershipTestTenant || proof.OrganizationID != "123456789" || proof.State != "active" || proof.Admission != "unassessed" {
			t.Fatal("retained native ID replaced the verified tenant", purpose, err)
		}
		if (purpose == "profile") != (proof.Profile != nil) {
			t.Fatal("profile purpose was not respected", purpose)
		}
	}
	before := calls.Load()
	if _, err := client.IdentityProjection(t.Context(), membershipTestTenant, "123456789", "retained-user", "membership"); !errors.Is(err, ErrIdentityUnavailable) || calls.Load() != before {
		t.Fatal("retained self-read compatibility queried new-grant authority", err)
	}
}
