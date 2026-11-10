package zitadel

import (
	"errors"
	"testing"
)

func TestSignupEmailRequiresNativeProofAndDoesNotSendAnAdministrativeOverride(t *testing.T) {
	rec := newRecorder()
	client := newClient(t, rec)
	if err := client.VerifySignupEmail(t.Context(), "owner-test", "native-code"); err != nil {
		t.Fatal(err)
	}
	got := rec.find("POST", "/v2/users/owner-test/email/verify")
	if got == nil || got.body["verificationCode"] != "native-code" || got.body["isVerified"] != nil {
		t.Fatal("missing native proof or privileged verification override")
	}
	before := len(rec.calls)
	if err := client.VerifySignupEmail(t.Context(), "owner-test", ""); !errors.Is(err, ErrSignupProof) {
		t.Fatalf("empty code accepted: %v", err)
	}
	if len(rec.calls) != before {
		t.Fatal("empty proof reached privileged provider API")
	}
	rec.status["/v2/users/owner-test/email/verify"] = 400
	if err := client.VerifySignupEmail(t.Context(), "owner-test", "wrong"); !errors.Is(err, ErrSignupProof) {
		t.Fatalf("native refusal was not preserved: %v", err)
	}
}

func TestSignupPasswordSetupNeverUsesCodeFreeAdministrativePasswordSetting(t *testing.T) {
	rec := newRecorder()
	client := newClient(t, rec)
	if err := client.SetSignupPassword(t.Context(), "owner-test", "native-code", "example-password"); err != nil {
		t.Fatal(err)
	}
	got := rec.find("POST", "/v2/users/owner-test/password")
	if got == nil || got.body["verificationCode"] != "native-code" || got.body["currentPassword"] != nil {
		t.Fatal("password setup omitted the native reset proof")
	}
	password, _ := got.body["newPassword"].(map[string]any)
	if password["password"] != "example-password" || password["changeRequired"] != false {
		t.Fatal("native password command was changed")
	}
	before := len(rec.calls)
	if err := client.SetSignupPassword(t.Context(), "owner-test", "", "example-password"); !errors.Is(err, ErrSignupProof) {
		t.Fatalf("code-free password setup allowed: %v", err)
	}
	if len(rec.calls) != before {
		t.Fatal("code-free setup reached identity provider")
	}
	rec.status["/v2/users/owner-test/password"] = 503
	if err := client.SetSignupPassword(t.Context(), "owner-test", "native-code", "example-password"); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("provider outage became password success: %v", err)
	}
}

func TestSignupPasswordLinkReturnsToHelivantaWithoutReturningACode(t *testing.T) {
	rec := newRecorder()
	client := newClient(t, rec)
	if err := client.SendSignupPasswordLink(t.Context(), "owner-test"); err != nil {
		t.Fatal(err)
	}
	got := rec.find("POST", "/v2/users/owner-test/password_reset")
	if got == nil || got.body["returnCode"] != nil {
		t.Fatal("password link did not use native email delivery")
	}
	link, _ := got.body["sendLink"].(map[string]any)
	if link["urlTemplate"] != "https://helivanta.app/signup/verify#phase=password&userId={{.UserID}}&code={{.Code}}" || link["notificationType"] != "NOTIFICATION_TYPE_Email" {
		t.Fatal("password link escaped the Helivanta flow")
	}
}

func TestSignupProofBindsTheExactActiveHumanHomeOrganizationAndEmail(t *testing.T) {
	rec := newRecorder()
	client := newClient(t, rec)
	rec.replies["/v2/users/owner-test"] = map[string]any{"user": map[string]any{"userId": "owner-test", "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": "org-test"}, "human": map[string]any{"email": map[string]any{"email": "owner@example.test", "isVerified": false}}}}
	verified, err := client.SignupIdentity(t.Context(), "org-test", "owner-test", "owner@example.test")
	if err != nil || verified || rec.find("GET", "/v2/users/owner-test") == nil {
		t.Fatalf("unverified native state was guessed: verified=%v err=%v", verified, err)
	}
	if _, err = client.SignupIdentity(t.Context(), "foreign-org", "owner-test", "owner@example.test"); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("foreign organization bound: %v", err)
	}
	if _, err = client.SignupIdentity(t.Context(), "org-test", "owner-test", "foreign@example.test"); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("foreign email bound: %v", err)
	}
	rec.replies["/v2/users/owner-test"] = map[string]any{"user": map[string]any{"userId": "owner-test", "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": "org-test"}}}
	if _, err = client.SignupIdentity(t.Context(), "org-test", "owner-test", "owner@example.test"); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("machine account accepted as human owner: %v", err)
	}
}
