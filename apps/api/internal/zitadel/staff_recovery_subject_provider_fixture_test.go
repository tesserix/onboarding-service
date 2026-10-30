//go:build staff_provider_fixture

package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestStaffRecoverySubjectProjectionFaultActualProvider(t *testing.T) {
	root, org, pat := loadStaffProviderFixture(t)
	name := os.Getenv("STAFF_RECOVERY_SUBJECT_CAPTURE")
	container := os.Getenv("STAFF_PROVIDER_FAULT_CONTAINER")
	if !strings.HasPrefix(name, "recovery-subject-TEST-recovery-") || filepath.Base(name) != name || !strings.HasPrefix(container, "staff-projection-db-") || !strings.HasPrefix(os.Getenv("DOCKER_HOST"), "unix://") {
		t.Fatal("explicit private subject capture and isolated local Docker fixture required")
	}
	info, err := os.Stat(filepath.Join(root, name))
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("private subject capture unavailable")
	}
	raw, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal("private subject capture unavailable")
	}
	var captured struct {
		Input      StaffIdentityInput `json:"input"`
		PreparedAt time.Time          `json:"prepared_at"`
	}
	if decodeIdentityJSON(raw, &captured) != nil || captured.Input.Validate() != nil || captured.Input.OrganizationID != org {
		t.Fatal("private subject capture invalid")
	}
	provider := New("http://localhost:32880", StaticToken(strings.TrimSpace(string(pat))), org, nil)
	proof, err := provider.InspectStaffRecoverySubject(t.Context(), captured.Input, captured.PreparedAt)
	if err != nil || proof.Disposition != "matching" {
		t.Fatalf("selected source fixture is not a matching retained subject: %v", err)
	}
	psql := func(input string, commands ...string) ([]byte, error) {
		args := []string{"exec", "-i", container, "psql", "-U", "zitadel", "-d", "zitadel", "-v", "ON_ERROR_STOP=1", "-qAt", "-1"}
		for _, command := range commands {
			args = append(args, "-c", command)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.Stdin = strings.NewReader(input)
		return cmd.Output()
	}
	tables := []string{"users14", "users14_humans", "users14_machines", "users14_notifications"}
	rows := map[string]string{}
	literal := "'" + strings.ReplaceAll(captured.Input.UserID, "'", "''") + "'"
	for _, table := range tables {
		column := "user_id"
		if table == "users14" {
			column = "id"
		}
		data, err := psql("", "COPY (SELECT * FROM projections."+table+" WHERE "+column+"="+literal+") TO STDOUT")
		if err != nil {
			t.Fatal("source projection capture failed before mutation")
		}
		rows[table] = string(data)
	}
	if bytes.Count([]byte(rows["users14"]), []byte("\n")) != 1 {
		t.Fatal("source projection target must be exactly one synthetic row")
	}
	backup, err := json.Marshal(map[string]any{"input": captured.Input, "prepared_at": captured.PreparedAt, "rows": rows})
	if err != nil {
		t.Fatal("source projection backup unavailable")
	}
	backupPath := filepath.Join(root, "recovery-subject-projection-fault-"+uuid.NewString()+".json")
	file, err := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal("private source projection backup unavailable")
	}
	_, writeErr := file.Write(backup)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal("private source projection backup incomplete")
	}
	restore := func() bool {
		for _, table := range tables {
			if rows[table] == "" {
				continue
			}
			columns, err := psql("", "SELECT string_agg(quote_ident(column_name),',' ORDER BY ordinal_position) FROM information_schema.columns WHERE table_schema='projections' AND table_name='"+table+"' AND is_generated='NEVER'")
			if err != nil || strings.TrimSpace(string(columns)) == "" {
				t.Errorf("source restoration schema unavailable; private capture: %s", backupPath)
				return false
			}
			selected := strings.TrimSpace(string(columns))
			if _, err := psql(rows[table], "CREATE TEMP TABLE recovery_restore (LIKE projections."+table+" INCLUDING DEFAULTS)",
				"COPY recovery_restore FROM STDIN", "INSERT INTO projections."+table+" ("+selected+") SELECT "+selected+" FROM recovery_restore ON CONFLICT DO NOTHING"); err != nil {
				t.Errorf("source projection restoration failed; private capture: %s", backupPath)
				return false
			}
		}
		return true
	}
	restored := false
	t.Cleanup(func() {
		if !restored {
			restore()
		}
	})
	deleted, err := psql("", "WITH gone AS (DELETE FROM projections.users14 WHERE id="+literal+" RETURNING 1) SELECT count(*) FROM gone")
	if err != nil || strings.TrimSpace(string(deleted)) != "1" {
		t.Fatal("isolated projection fault did not affect exactly one target")
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, provider.base+"/v2/users/"+captured.Input.UserID, nil)
	if err != nil {
		t.Fatal("source fault probe unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(pat)))
	req.Header.Set("x-zitadel-orgid", org)
	response, err := provider.http.Do(req)
	if err != nil {
		t.Fatal("source fault probe unavailable")
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("isolated missing projection did not return actual 404: HTTP %d", response.StatusCode)
	}
	proof, err = provider.InspectStaffRecoverySubject(t.Context(), captured.Input, captured.PreparedAt)
	if !errors.Is(err, ErrIdentityUnavailable) || proof != (StaffRecoverySubjectProof{}) {
		t.Fatal("actual stale source 404 authorized replacement absence")
	}
	if !restore() {
		return
	}
	restored = true
	for _, table := range tables {
		column := "user_id"
		if table == "users14" {
			column = "id"
		}
		data, err := psql("", "COPY (SELECT * FROM projections."+table+" WHERE "+column+"="+literal+") TO STDOUT")
		if err != nil || !bytes.Equal(data, []byte(rows[table])) {
			t.Fatal("restored source relation differs from captured synthetic fixture")
		}
	}
	proof, err = provider.InspectStaffRecoverySubject(t.Context(), captured.Input, captured.PreparedAt)
	if err != nil || proof.Disposition != "matching" {
		t.Fatalf("source projection restoration did not recover matching subject: %v", err)
	}
	t.Log("actual stale 404 rejected; all captured projection rows restored; backup retained privately")
}
