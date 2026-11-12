package staffcontract

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStaffRecipientRejectsCredentialAuthorityAndMalformedCapabilities(t *testing.T) {
	base := StaffRecipientInput{InvitationID: uuid.Must(uuid.NewV7()), Ticket: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	for _, action := range []string{"status", "email", "password", "resend"} {
		in := base
		switch action {
		case "email":
			in.Code = "opaque+Code/==#"
		case "password":
			in.Code = "opaque+Code/==#"
			in.Password = "private-password"
		case "resend":
			id := uuid.Must(uuid.NewV7())
			in.DeliveryID = &id
		}
		if in.Validate(action) != nil {
			t.Fatalf("valid %s rejected", action)
		}
		for _, mutate := range []func(*StaffRecipientInput){
			func(v *StaffRecipientInput) { v.InvitationID = uuid.New() },
			func(v *StaffRecipientInput) { v.Ticket += "=" },
			func(v *StaffRecipientInput) { v.Ticket = "" },
			func(v *StaffRecipientInput) { v.Code = "code\n" },
			func(v *StaffRecipientInput) { v.Password = strings.Repeat("x", 201) },
		} {
			wrong := in
			mutate(&wrong)
			if wrong.Validate(action) == nil {
				t.Fatalf("malformed %s accepted", action)
			}
		}
		for _, secret := range []string{in.Ticket, in.Password, in.Code} {
			if secret != "" && strings.Contains(fmt.Sprintf("%v %#v", in, in), secret) {
				t.Fatal("recipient diagnostic leaked proof")
			}
		}
	}
	for _, action := range []string{"email", "password", "resend", "owner", "isVerified"} {
		if base.Validate(action) == nil {
			t.Fatal("missing proof accepted")
		}
	}
	for _, state := range []string{"complete", "delivery_unresolved", "proof_unresolved", "already_verified", "consumed", "expired", "email_action_required", "password_action_required"} {
		expiry := time.Now().UTC().Add(time.Minute)
		v := StaffInvitationReceipt{State: state, InvitationID: &base.InvitationID, Phase: "email", ExpiresAt: &expiry}
		if v.Validate() != nil {
			t.Fatal("valid receipt rejected")
		}
		v.InvitationID = nil
		if state != "complete" && v.Validate() == nil {
			t.Fatal("unbound receipt accepted")
		}
	}
	if (StaffInvitationReceipt{State: "complete"}).Validate() != nil || (StaffInvitationReceipt{State: "owner"}).Validate() == nil {
		t.Fatal("receipt state validation failed")
	}
}

func TestStaffRecipientDecoderRejectsDuplicateUnknownAndWrongPhaseFields(t *testing.T) {
	in := StaffRecipientInput{InvitationID: uuid.Must(uuid.NewV7()), Ticket: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeRecipient(body, "status"); err != nil {
		t.Fatal("canonical status does not round trip")
	}
	for _, raw := range []string{
		strings.TrimSuffix(string(body), "}") + `,"password":""}`,
		strings.TrimSuffix(string(body), "}") + `,"ticket":"` + in.Ticket + `"}`,
		strings.Replace(string(body), "ticket", "Ticket", 1),
		string(body) + `{}`,
		string(body) + string([]byte{255}),
	} {
		if _, err = DecodeRecipient([]byte(raw), "status"); err == nil {
			t.Fatal("ambiguous or wrong-phase proof accepted")
		}
	}
}
