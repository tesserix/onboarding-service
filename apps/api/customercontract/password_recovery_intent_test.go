package customercontract

import (
	"encoding/json"
	"testing"
)

func TestPasswordRecoveryIntentHasNoCredentialPayloadAndStrictCommandSelection(t *testing.T) {
	const command = "019f39c2-1738-7998-b5de-6d4214f614e0"
	const challenge = "019f39c2-1738-7998-b5de-6d4214f614e1"
	const browser = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	good := `{"commandId":"` + command + `","id":"` + challenge + `","browserHash":"` + browser + `"}`
	var intent PasswordRecoveryIntent
	if err := json.Unmarshal([]byte(good), &intent); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		good[:len(good)-1] + `,"password":"never accepted here"}`,
		good[:len(good)-1] + `,"commandId":"` + command + `"}`,
		`{"command_id":"` + command + `","id":"` + challenge + `","browserHash":"` + browser + `"}`,
		`{"commandId":null,"id":"` + challenge + `","browserHash":"` + browser + `"}`,
	} {
		if json.Unmarshal([]byte(bad), &intent) == nil {
			t.Fatal("ambiguous intent accepted")
		}
	}
	for _, state := range []string{"prepared", "cancelled"} {
		var result PasswordRecoveryResult
		if err := json.Unmarshal([]byte(`{"state":"`+state+`","commandId":"`+command+`"}`), &result); err != nil {
			t.Fatal(err)
		}
	}
	var status PasswordRecoveryStatus
	if err := json.Unmarshal([]byte(`{"state":"prepared","commandId":"not-a-command"}`), &status); err == nil {
		t.Fatal("invalid private selector accepted")
	}
	if err := json.Unmarshal([]byte(`{"state":"ready","commandId":"`+command+`"}`), &status); err == nil {
		t.Fatal("ready receipt invented a command")
	}
}

func TestCancellationRestoresCommandFromPrivateBrowserMarker(t *testing.T) {
	var in PasswordRecoveryCancellation
	for _, raw := range []string{`{}`, `{"command_id":"019f39c2-1738-7998-b5de-6d4214f614e0"}`} {
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`{"command_id":null}`, `{"password":"forbidden"}`, `{"command_id":"bad"}`} {
		if json.Unmarshal([]byte(raw), &in) == nil {
			t.Fatal("ambiguous cancellation accepted")
		}
	}
}
