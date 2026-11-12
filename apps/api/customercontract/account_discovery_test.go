package customercontract

import "testing"

func TestAccountDiscoveryRejectsUnboundedOrAmbiguousInputs(t *testing.T) {
	valid := DiscoveryStart{ID: "019a71f0-1234-7000-8000-000000000001", BrowserHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SourceHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Email: "owner@example.test"}
	if valid.Validate() != nil {
		t.Fatal("valid start rejected")
	}
	for _, email := range []string{"", "Name <owner@example.test>", " owner@example.test", "owner@example.test\n"} {
		in := valid
		in.Email = email
		if in.Validate() == nil {
			t.Fatal("ambiguous email accepted")
		}
	}
	proof := DiscoveryProof{ID: valid.ID, BrowserHash: valid.BrowserHash, Code: "12345678"}
	if proof.Validate() != nil {
		t.Fatal("valid proof rejected")
	}
	for _, code := range []string{"1234567", "123456789", "1234567x", "１２３４５６７８"} {
		proof.Code = code
		if proof.Validate() == nil {
			t.Fatal("invalid proof accepted")
		}
	}
}
