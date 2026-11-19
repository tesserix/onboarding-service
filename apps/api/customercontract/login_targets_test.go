package customercontract

import (
	"fmt"
	"testing"
)

func TestLoginTargetsBoundsCandidatesAndCarriesNoneWhenTruncated(t *testing.T) {
	in := LoginTargetsInput{Email: "same@example.test", RequestID: "11111111-1111-4111-8111-111111111112"}
	if in.Validate() != nil {
		t.Fatal("valid lookup refused")
	}
	for _, email := range []string{"", "name <same@example.test>", " same@example.test"} {
		if (LoginTargetsInput{Email: email, RequestID: in.RequestID}).Validate() == nil {
			t.Fatal("invalid email accepted", email)
		}
	}
	candidate := func(i int) LoginCandidate {
		tenant := fmt.Sprintf("11111111-1111-4111-8111-%012d", i+1)
		return LoginCandidate{TenantID: tenant, OrganizationID: "helivanta-" + tenant, Subject: "staff", Name: "Hospital", Account: in.Email}
	}
	five := []LoginCandidate{}
	for i := range MaxLoginTargets {
		five = append(five, candidate(i))
	}
	if (LoginTargets{Targets: five}).Validate() != nil || (LoginTargets{Targets: []LoginCandidate{}, Truncated: true}).Validate() != nil {
		t.Fatal("valid answer refused")
	}
	foreign := candidate(0)
	foreign.OrganizationID = "helivanta-" + candidate(1).TenantID
	for name, bad := range map[string]LoginTargets{
		"nil":               {},
		"six":               {Targets: append(five, candidate(5))},
		"truncated w/ list": {Targets: five[:1], Truncated: true},
		"duplicate tenant":  {Targets: []LoginCandidate{candidate(0), candidate(0)}},
		"foreign binding":   {Targets: []LoginCandidate{foreign}},
		"blank name":        {Targets: []LoginCandidate{{TenantID: five[0].TenantID, OrganizationID: five[0].OrganizationID, Subject: "staff", Account: in.Email}}},
	} {
		if bad.Validate() == nil {
			t.Fatal("invalid answer accepted:", name)
		}
	}
}
