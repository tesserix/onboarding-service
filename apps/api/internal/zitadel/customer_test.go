package zitadel

import (
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestExistingEmailIsNotUsedToAdoptAnotherPersonAsTenantOwner(t *testing.T) {
	rec := newRecorder()
	client := newClient(t, rec)
	rec.replies["/v2/users"] = map[string]any{"details": map[string]string{"totalResult": "1"}, "result": []map[string]any{{"userId": "other-person", "human": map[string]any{"email": map[string]string{"email": "owner@example.test"}}}}}
	existing, err := client.OtherSignupIdentity(t.Context(), "owner@example.test", "owner-test")
	if err != nil || !existing {
		t.Fatalf("existing email was treated as a fresh owner: existing=%v err=%v", existing, err)
	}
	got := rec.find("POST", "/v2/users")
	if got == nil {
		t.Fatal("no exact bounded native email lookup")
	}
	query, _ := got.body["query"].(map[string]any)
	if query["limit"] != "2" {
		t.Fatal("unbounded directory lookup")
	}
	rec.replies["/v2/users"] = map[string]any{"details": map[string]string{"totalResult": "0"}, "result": []any{}}
	existing, err = client.OtherSignupIdentity(t.Context(), "owner@example.test", "owner-test")
	if err != nil || existing {
		t.Fatalf("new email refused: %v", err)
	}
}

// One-shot signup (helivanta#1763): a password the native policy refuses is
// told apart from a bad proof, so the caller can keep the link and retry.
func TestSignupPasswordPolicyRefusalIsDistinctFromABadProof(t *testing.T) {
	body := `{"code":3,"message":"Errors.User.PasswordComplexityPolicy.MinLength (DOMAIN-HuJf6)","details":[{"@type":"type.googleapis.com/zitadel.v1.ErrorDetail","id":"DOMAIN-HuJf6","message":"Errors.User.PasswordComplexityPolicy.MinLength"}]}`
	policy := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		if policy {
			_, _ = w.Write([]byte(body))
			return
		}
		_, _ = w.Write([]byte(`{"code":3,"message":"Errors.User.Code.Invalid (CODE-woT0xc)","details":[{"@type":"type.googleapis.com/zitadel.v1.ErrorDetail","id":"CODE-woT0xc"}]}`))
	}))
	t.Cleanup(srv.Close)
	client := New(srv.URL, StaticToken("synthetic"), "platform", nil)
	if err := client.SetSignupPassword(t.Context(), "owner-test", "reset-1", "short"); !errors.Is(err, ErrSignupPasswordPolicy) {
		t.Fatalf("policy refusal not distinguished: %v", err)
	}
	policy = false
	if err := client.SetSignupPassword(t.Context(), "owner-test", "reset-1", "Long-enough-pass-1!"); !errors.Is(err, ErrSignupProof) || errors.Is(err, ErrSignupPasswordPolicy) {
		t.Fatalf("bad code reported as policy: %v", err)
	}
}
