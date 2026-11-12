//go:build staff_provider_fixture

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// This explicitly selected fixture uses real provider I/O and fixture registry proof.
// It is not recipient verification or normal-role UI acceptance.
func TestStaffIdentityCreationUsesActualLocalProvider(t *testing.T) {
	dsn, err := url.Parse(os.Getenv("ONBOARDING_TEST_DSN"))
	if err != nil || dsn.Hostname() != "127.0.0.1" || dsn.Port() != "32768" || dsn.Path != "/postgres" {
		t.Fatal("explicit disposable loopback ledger fixture is required")
	}
	root := os.Getenv("STAFF_PROVIDER_FIXTURE_PATH")
	if !filepath.IsAbs(root) {
		t.Fatal("explicit private local-provider fixture directory is required")
	}
	state, err := os.ReadFile(filepath.Join(root, "probe-state.json"))
	if err != nil {
		t.Fatal("private local fixture state unavailable")
	}
	var fixture struct{ Org string }
	if json.Unmarshal(state, &fixture) != nil || fixture.Org == "" {
		t.Fatal("invalid private fixture state")
	}
	patPath := filepath.Join(root, "secrets", "qa.pat")
	stat, err := os.Stat(patPath)
	if err != nil || stat.Mode().Perm()&0077 != 0 {
		t.Fatal("fixture credential must remain owner-readable only")
	}
	pat, err := os.ReadFile(patPath)
	if err != nil {
		t.Fatal("private fixture credential unavailable")
	}
	provider := zitadel.New("http://localhost:32880", zitadel.StaticToken(strings.TrimSpace(string(pat))), "", nil)
	h := newHarness(t, onboarding.WithStaffIdentity("planning-poker", provider))
	tenant := identityOrg(t, h)
	if _, err := h.fixturePool.Exec(t.Context(), `UPDATE organizations SET zitadel_org_id=$1 WHERE id=$2`, fixture.Org, tenant); err != nil {
		t.Fatal(err)
	}
	h.identity.verify = func(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
		return identityObservation(org, tenant, project), nil
	}
	command := store.StaffIdentityCommand{ID: uuid.Must(uuid.NewV7()), OrgID: tenant, ActorSubject: "TEST-admin"}
	t.Cleanup(func() {
		for _, item := range []struct{ table, column string }{{"staff_identity_creations", "command_id"}, {"staff_identity_dispatches", "command_id"}, {"staff_identity_contact_claims", "root_command_id"}, {"staff_identity_commands", "id"}} {
			if _, err := h.fixturePool.Exec(context.Background(), "DELETE FROM "+item.table+" WHERE "+item.column+"=$1", command.ID); err != nil {
				t.Error(err)
			}
		}
	})
	email := "role-free-" + command.ID.String() + "@example.invalid"
	raw, err := json.Marshal(map[string]string{"actor_subject": command.ActorSubject, "email": email, "given_name": "Synthetic", "family_name": "RoleFree", "reason": "Explicit isolated owner/provider protocol acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	first := h.do(t, http.MethodPut, staffIdentityPath(command), productToken, string(raw))
	if first.Code != http.StatusOK {
		t.Fatalf("actual provider creation HTTP %d", first.Code)
	}
	body := decode(t, first)
	user, ok := body["user_id"].(string)
	if !ok || user == "" || body["command_state"] != "created" {
		t.Fatal("actual provider creation did not retain exact receipt")
	}
	assertLocalStaffIdentityHasNoGrants(t, strings.TrimSpace(string(pat)), fixture.Org, user)
	second := h.do(t, http.MethodPut, staffIdentityPath(command), productToken, string(raw))
	if second.Code != http.StatusOK || second.Body.String() != first.Body.String() {
		t.Fatal("actual-provider retry changed the durable receipt")
	}
	observed := h.do(t, http.MethodPost, staffIdentityPath(command)+"/observations", productToken, `{"actor_subject":"TEST-admin"}`)
	if observed.Code != http.StatusOK {
		t.Fatalf("actual provider observation HTTP %d", observed.Code)
	}
	current, ok := decode(t, observed)["observation"].(map[string]any)
	if !ok || current["email_verified"] != false {
		t.Fatal("ordinary creation falsely verified the contact")
	}
	var nonce string
	if err := h.fixturePool.QueryRow(t.Context(), `SELECT binding_nonce FROM staff_identity_commands WHERE id=$1`, command.ID).Scan(&nonce); err != nil {
		t.Fatal(err)
	}
	evidence := map[string]any{"owner_command": command.ID.String(), "fixture_tenant": tenant.String(), "source_org": fixture.Org, "source_user": user, "email": email, "binding_nonce": nonce, "password": nil, "email_verified": false, "account_setup_required": true, "source_memberships": 0, "source_user_grants": 0, "receipt": json.RawMessage(first.Body.Bytes()), "observation": json.RawMessage(observed.Body.Bytes()), "recorded_at": time.Now().UTC(), "fixture_registry_proof": true, "normal_ui_acceptance": false}
	record, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "role-free-create-"+command.ID.String()+".json"), append(record, '\n'), 0600); err != nil {
		t.Fatal("could not retain private synthetic account evidence")
	}
}

func assertLocalStaffIdentityHasNoGrants(t *testing.T, token, org, user string) {
	t.Helper()
	queries := []struct {
		path string
		body map[string]any
	}{
		{"/management/v1/users/" + url.PathEscape(user) + "/memberships/_search", map[string]any{"query": map[string]any{"limit": 10}}},
		{"/management/v1/users/grants/_search", map[string]any{"query": map[string]any{"limit": 10}, "queries": []any{map[string]any{"userIdQuery": map[string]string{"userId": user}}}}},
	}
	client := http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for _, query := range queries {
		raw, err := json.Marshal(query.body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:32880"+query.path, bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-zitadel-orgid", org)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("actual source role query failed")
		}
		body, readErr := io.ReadAll(io.LimitReader(res.Body, 65537))
		closeErr := res.Body.Close()
		if readErr != nil || closeErr != nil || res.StatusCode != http.StatusOK || len(body) > 65536 {
			t.Fatal("actual source role query unavailable or oversized")
		}
		var proof struct{ Result []json.RawMessage }
		if json.Unmarshal(body, &proof) != nil || len(proof.Result) != 0 {
			t.Fatal("ordinary Staff creation acquired source permissions")
		}
	}
}
