package onboarding

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

func TestStaffInvitationReplaysExposeStateWithoutRequestingAnotherProof(t *testing.T) {
	now := time.Now().UTC()
	base := store.StaffInvitation{ID: uuid.Must(uuid.NewV7()), Phase: "email", ExpiresAt: now.Add(time.Minute), AcceptedAt: &now}
	for _, tc := range []struct {
		name, want           string
		mutate               func(*store.StaffInvitation)
		verified, configured bool
	}{
		{"live", "email_action_required", func(*store.StaffInvitation) {}, false, false},
		{"verified", "already_verified", func(*store.StaffInvitation) {}, true, false},
		{"configured", "complete", func(*store.StaffInvitation) {}, true, true},
		{"expired", "expired", func(v *store.StaffInvitation) { v.ExpiresAt = now.Add(-time.Minute) }, false, false},
		{"superseded", "superseded", func(v *store.StaffInvitation) { v.Superseded = true }, false, false},
		{"uncertain", "proof_unresolved", func(v *store.StaffInvitation) { v.ProofUncertain = true }, false, false},
		{"password", "password_action_required", func(v *store.StaffInvitation) { v.Phase = "password" }, true, false},
		{"recorded completion", "complete", func(v *store.StaffInvitation) { v.PasswordConfigured = true }, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := base
			tc.mutate(&v)
			r := invitationReceipt(v, tc.verified, tc.configured)
			if r.State != tc.want || r.Validate() != nil {
				t.Fatalf("state=%s validation=%v", r.State, r.Validate())
			}
		})
	}
}
