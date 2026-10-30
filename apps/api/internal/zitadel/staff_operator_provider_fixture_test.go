//go:build staff_provider_fixture

package zitadel

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Explicit source-protocol fixture; not human login or HMS reviewer acceptance.
func TestStaffRecoveryOperatorActualProvider(t *testing.T) {
	root := os.Getenv("STAFF_PROVIDER_FIXTURE_PATH")
	if !filepath.IsAbs(root) {
		t.Fatal("explicit isolated provider fixture is required")
	}
	raw, err := os.ReadFile(filepath.Join(root, "probe-state.json"))
	if err != nil {
		t.Fatal("private fixture state unavailable")
	}
	var fixture struct{ Org string }
	if json.Unmarshal(raw, &fixture) != nil || !identitySourceID.MatchString(fixture.Org) {
		t.Fatal("invalid fixture organization")
	}
	patPath := filepath.Join(root, "secrets", "qa.pat")
	stat, err := os.Stat(patPath)
	if err != nil || stat.Mode().Perm()&0077 != 0 {
		t.Fatal("private fixture credential permissions are required")
	}
	pat, err := os.ReadFile(patPath)
	if err != nil {
		t.Fatal("private fixture credential unavailable")
	}
	client := &http.Client{Timeout: 4 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	call := func(method, path string, in any) map[string]json.RawMessage {
		t.Helper()
		body, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(t.Context(), method, "http://localhost:32880"+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal("fixture request unavailable")
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(pat)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-zitadel-orgid", fixture.Org)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("isolated provider unavailable")
		}
		defer func() { _ = res.Body.Close() }()
		data, err := io.ReadAll(io.LimitReader(res.Body, identityReadLimit+1))
		if err != nil || len(data) > identityReadLimit || res.StatusCode != http.StatusOK {
			t.Fatalf("isolated provider protocol status %d at %s", res.StatusCode, path)
		}
		var result map[string]json.RawMessage
		if decodeIdentityJSON(data, &result) != nil {
			t.Fatal("invalid isolated provider result")
		}
		return result
	}
	id := func(result map[string]json.RawMessage, key string) string {
		t.Helper()
		var value string
		if json.Unmarshal(result[key], &value) != nil || !identitySourceID.MatchString(value) {
			t.Fatal("missing isolated provider object identity")
		}
		return value
	}
	label := "recovery-authority-" + uuid.NewString()
	project := id(call(http.MethodPost, "/management/v1/projects", map[string]any{"name": label}), "id")
	for _, key := range []string{"platform", "rotate-credentials"} {
		call(http.MethodPost, "/management/v1/projects/"+project+"/roles", map[string]string{"roleKey": key, "displayName": key})
	}
	subject := id(call(http.MethodPost, "/v2/users/human", map[string]any{
		"organizationId": fixture.Org, "username": label + "@example.invalid",
		"profile": map[string]string{"givenName": "Synthetic", "familyName": "RecoveryOperator"},
		"email":   map[string]any{"email": label + "@example.invalid", "isVerified": true},
	}), "userId")
	grant := id(call(http.MethodPost, "/management/v1/users/"+subject+"/grants", map[string]any{"projectId": project, "roleKeys": []string{"platform", "rotate-credentials"}}), "userGrantId")
	provider := New("http://localhost:32880", StaticToken(strings.TrimSpace(string(pat))), fixture.Org, nil)
	proof, err := provider.VerifyStaffRecoveryOperator(t.Context(), subject, project, true)
	if err != nil || proof.Subject != subject || proof.GrantID != grant || !proof.Write {
		t.Fatalf("actual current operator authority unavailable: %v", err)
	}
	call(http.MethodPost, "/management/v1/users/"+subject+"/grants/"+grant+"/_deactivate", map[string]any{})
	proof, err = provider.VerifyStaffRecoveryOperator(t.Context(), subject, project, true)
	if err == nil || proof != (StaffRecoveryOperatorProof{}) {
		t.Fatal("actual withdrawn grant retained recovery authority")
	}
	call(http.MethodPost, "/management/v1/users/"+subject+"/grants/"+grant+"/_reactivate", map[string]any{})
	proof, err = provider.VerifyStaffRecoveryOperator(t.Context(), subject, project, true)
	if err != nil || proof.Subject != subject {
		t.Fatalf("actual restored grant unavailable: %v", err)
	}
	call(http.MethodDelete, "/management/v1/projects/"+project+"/roles/rotate-credentials", nil)
	proof, err = provider.VerifyStaffRecoveryOperator(t.Context(), subject, project, true)
	if err == nil || proof != (StaffRecoveryOperatorProof{}) {
		t.Fatal("actual removed role definition retained write authority")
	}
	proof, err = provider.VerifyStaffRecoveryOperator(t.Context(), subject, project, false)
	if err != nil || proof.Subject != subject || proof.Write {
		t.Fatalf("actual remaining read-only authority unavailable: %v", err)
	}
	call(http.MethodPost, "/management/v1/users/"+subject+"/_deactivate", map[string]any{})
	proof, err = provider.VerifyStaffRecoveryOperator(t.Context(), subject, project, false)
	if (!errors.Is(err, ErrStaffOperatorDenied) && !errors.Is(err, ErrIdentityUnavailable)) || proof != (StaffRecoveryOperatorProof{}) {
		t.Fatal("actual disabled identity retained read authority")
	}
}
