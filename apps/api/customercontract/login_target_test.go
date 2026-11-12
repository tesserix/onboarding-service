package customercontract

import "testing"

func TestPrivateLoginTargetRequiresExactTenantAndBoundedMailbox(t *testing.T) {
	in := LoginTargetInput{TenantID: "11111111-1111-4111-8111-111111111111", RequestID: "11111111-1111-4111-8111-111111111112", Email: "same@example.test"}
	if in.Validate() != nil {
		t.Fatal("valid lookup refused")
	}
	for _, email := range []string{"", "name <same@example.test>", " same@example.test", "same\n@example.test"} {
		bad := in
		bad.Email = email
		if bad.Validate() == nil {
			t.Fatal("invalid email accepted")
		}
	}
	bad := in
	bad.TenantID = "00000000-0000-0000-0000-000000000000"
	if bad.Validate() == nil {
		t.Fatal("empty tenant accepted")
	}
	target := LoginTarget{TenantID: in.TenantID, OrganizationID: "helivanta-" + in.TenantID, Subject: "staff"}
	if target.Validate() != nil {
		t.Fatal("valid binding refused")
	}
	target.OrganizationID = "foreign"
	if target.Validate() == nil {
		t.Fatal("foreign binding accepted")
	}
}
