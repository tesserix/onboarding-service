package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestStaffContractsFollowCanonicalJSONNamesAndOptionality(t *testing.T) {
	output, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	text := string(output)
	for _, expected := range []string{"export type StaffIdentityRecoveryRequestReceipt = {", "request_id: string;", "requested_action: string;", "decision?: StaffIdentityRecoveryDecisionReceipt;", "successor_command_id?: string;", "observed_at: string;", "schema_version: number;"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing canonical field %s", expected)
		}
	}
	for _, private := range []string{"binding_nonce", "provider_org_id", "machine_subject", "original_user_id"} {
		if strings.Contains(text, private) {
			t.Fatalf("private source binding leaked: %s", private)
		}
	}
	again, err := generate()
	if err != nil || string(again) != text {
		t.Fatal("contract generation is not deterministic")
	}
}

func TestRetainedStaffContractsAreCurrent(t *testing.T) {
	generated, err := generate()
	if err != nil {
		t.Fatal(err)
	}
	retained, err := os.ReadFile("../../../../contracts/staff-recovery.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(retained, generated) {
		t.Fatal("run go run ./cmd/staff-contracts from apps/api to regenerate canonical consumer receipts")
	}
}
