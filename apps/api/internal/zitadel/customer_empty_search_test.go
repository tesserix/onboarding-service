package zitadel

import (
	"errors"
	"testing"
)

func TestSignupEmailLookupAcceptsNativeOmittedZerosOnlyWithValidListMetadata(t *testing.T) {
	for _, tc := range []struct {
		name  string
		reply map[string]any
		valid bool
	}{
		{"native omitted zero count and empty results", map[string]any{"details": map[string]any{"timestamp": "2026-11-11T05:36:41.870629Z"}}, true},
		{"omitted count with explicit empty results", map[string]any{"details": map[string]any{"timestamp": "2026-11-11T05:36:41Z"}, "result": []any{}}, true},
		{"missing list metadata", map[string]any{}, false},
		{"empty list metadata", map[string]any{"details": map[string]any{}}, false},
		{"invalid timestamp", map[string]any{"details": map[string]any{"timestamp": "invalid"}}, false},
		{"explicit invalid empty count", map[string]any{"details": map[string]any{"timestamp": "2026-11-11T05:36:41Z", "totalResult": ""}}, false},
		{"omitted count with a person", map[string]any{"details": map[string]any{"timestamp": "2026-11-11T05:36:41Z"}, "result": []any{map[string]any{"userId": "foreign-person", "human": map[string]any{"email": map[string]any{"email": "owner@example.test"}}}}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := newRecorder()
			recorder.replies["/v2/users"] = tc.reply
			client := newClient(t, recorder)
			exists, err := client.OtherSignupIdentity(t.Context(), "owner@example.test", "owner-test")
			if tc.valid {
				if err != nil || exists {
					t.Fatalf("native empty search rejected: exists=%v err=%v", exists, err)
				}
			} else if !errors.Is(err, ErrIdentityUnavailable) {
				t.Fatalf("ambiguous native result accepted: %v", err)
			}
		})
	}
}
