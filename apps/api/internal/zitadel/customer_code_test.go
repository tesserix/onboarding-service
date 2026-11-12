package zitadel

import (
	"errors"
	"testing"
)

func TestPrivateSignupCodeUsesNativeReturnModeWithoutSendingSMTP(t *testing.T) {
	for _, password := range []bool{false, true} {
		rec := newRecorder()
		client := newClient(t, rec)
		path := "/v2/users/owner-test/email/send"
		if password {
			path = "/v2/users/owner-test/password_reset"
		}
		rec.replies[path] = map[string]any{"verificationCode": "native-code"}
		code, err := client.SignupCode(t.Context(), "owner-test", password)
		if err != nil || code != "native-code" {
			t.Fatalf("native code missing: %v", err)
		}
		call := rec.find("POST", path)
		if call == nil || call.body["returnCode"] == nil || call.body["sendCode"] != nil || call.body["sendLink"] != nil {
			t.Fatal("private request used native SMTP")
		}
		rec.replies[path] = map[string]any{"verificationCode": ""}
		if _, err := client.SignupCode(t.Context(), "owner-test", password); !errors.Is(err, ErrIdentityUnavailable) {
			t.Fatal("missing code treated as success")
		}
	}
}
