package zitadel

import (
	"context"
	"net/http"
	"testing"
)

func TestExistingWorkspaceOwnerRequiresExactActiveVerifiedHuman(t *testing.T) {
	for _, tc := range []struct {
		name, email, state  string
		verified, human, ok bool
	}{
		{"valid", "owner@example.test", "USER_STATE_ACTIVE", true, true, true},
		{"different email", "other@example.test", "USER_STATE_ACTIVE", true, true, false},
		{"unverified", "owner@example.test", "USER_STATE_ACTIVE", false, true, false},
		{"inactive", "owner@example.test", "USER_STATE_INACTIVE", true, true, false},
		{"machine", "owner@example.test", "USER_STATE_ACTIVE", true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorder()
			user := map[string]any{"userId": "owner-1", "state": tc.state}
			if tc.human {
				user["human"] = map[string]any{"email": map[string]any{"email": tc.email, "isVerified": tc.verified}}
			}
			rec.replies["/v2/users/owner-1"] = map[string]any{"user": user}
			client := newClient(t, rec)
			err := client.VerifyExistingHuman(context.Background(), "owner-1", "owner@example.test")
			if (err == nil) != tc.ok {
				t.Fatalf("verification=%v expected ok=%v", err, tc.ok)
			}
			if call := rec.find(http.MethodGet, "/v2/users/owner-1"); call == nil {
				t.Fatal("identity was not checked")
			}
		})
	}
}
