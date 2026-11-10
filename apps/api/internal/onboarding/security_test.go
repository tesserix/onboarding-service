package onboarding

import (
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"testing"
)

func TestSecurityViewFingerprintsNativePolicyAndClaimForStaleRequests(t *testing.T) {
	in := customercontract.SecurityRequest{TenantID: "11111111-1111-4111-8111-111111111111", Subject: "owner", RequestID: "11111111-1111-4111-8111-111111111112"}
	claim := hospitalclaims.Result{ResourceVersion: "7"}
	proof := zitadel.OwnerSecurityProof{RequireMFA: true, ExternalMFA: true, NoGrace: true, Sequence: "13"}
	applied := securityView(in, claim, proof)
	if applied.Validate() != nil || applied.State != customercontract.SecurityApplied {
		t.Fatalf("invalid view %+v", applied)
	}
	proof.RequireMFA = false
	drift := securityView(in, claim, proof)
	if drift.State != customercontract.SecurityDrifted || drift.Revision == applied.Revision {
		t.Fatal("native drift reused an obsolete fingerprint")
	}
	claim.SecurityRequest = in.RequestID
	pending := securityView(in, claim, proof)
	if pending.State != customercontract.SecurityPending || pending.Revision == drift.Revision {
		t.Fatal("pending reconcile presented as applied")
	}
	proof.RequireMFA = true
	if securityView(in, claim, proof).State != customercontract.SecurityApplied {
		t.Fatal("fresh native enforcement was hidden")
	}
}
