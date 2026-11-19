//go:build identityfixture

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestFixtureRecoveryIsExplicitAndDoesNotExposeOtherCommands(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) })
	for _, enabled := range []bool{false, true} {
		handler := fixtureHandler(next, enabled)
		for _, action := range []string{"start", "verify", "status", "prepare", "cancel", "password"} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest("POST", "/internal/v1/customer-signups/password-recovery/"+action, nil))
			expected := 404
			if enabled {
				expected = 204
			}
			if rec.Code != expected {
				t.Fatal("recovery fixture was not explicitly gated", action, rec.Code)
			}
		}
		for _, path := range []string{"/internal/v1/customer-signups", "/internal/v1/customer-signups/password", "/internal/v1/customer-signups/password-recovery/delete", "/internal/v1/customer-signups/password-recovery/password/extra"} {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest("POST", path, nil))
			if rec.Code != 404 {
				t.Fatal("fixture widened command authority", path)
			}
		}
	}
}

func TestFixtureMailCapturesOnlyDedicatedSyntheticRecoveryWithoutNetwork(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "mailbox")
	mail, cleanup, err := newRecoveryMailbox(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	id := uuid.Must(uuid.NewV7()).String()
	email := "e2e-password-recovery-native-admin@helivanta.dev"
	if _, err = mail.SendPasswordRecovery(context.Background(), id, email, "01234567"); err != nil {
		t.Fatal("isolated recovery delivery failed")
	}
	file := filepath.Join(dir, id+".json")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var received struct{ Email, Code string }
	if json.Unmarshal(raw, &received) != nil || received.Email != email || received.Code != "01234567" {
		t.Fatal("isolated mailbox payload invalid")
	}
	info, err := os.Stat(file)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("mailbox proof permissions unsafe")
	}
	if _, err = mail.SendPasswordRecovery(context.Background(), id, email, "01234567"); err != nil {
		t.Fatal("same fixture delivery could not replay")
	}
	if _, err = mail.SendPasswordRecovery(context.Background(), id, email, "87654321"); err == nil {
		t.Fatal("delivery ID accepted a different proof")
	}
	if _, err = mail.SendPasswordRecovery(context.Background(), uuid.Must(uuid.NewV7()).String(), "real@example.com", "01234567"); err == nil {
		t.Fatal("non-fixture recipient was captured")
	}
	if _, err = mail.SendDiscovery(context.Background(), "real@example.com", "12345678"); err == nil {
		t.Fatal("fixture accepted another mail purpose")
	}
}

func TestFixtureMailboxRefusesSharedSymlinkAndRelativeDirectories(t *testing.T) {
	root := t.TempDir()
	shared := filepath.Join(root, "shared")
	if err := os.Mkdir(shared, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(shared, link); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"relative", shared, link} {
		_, cleanup, err := newRecoveryMailbox(dir)
		if cleanup != nil {
			cleanup()
		}
		if err == nil {
			t.Fatal("unsafe mailbox path accepted", strings.TrimPrefix(dir, root))
		}
	}
}

func TestFixtureMailTransportNeverFallsBackToAnExternalEndpoint(t *testing.T) {
	capture := recoveryMailCapture{dir: t.TempDir()}
	for _, target := range []string{"https://api.resend.com/emails?redirect=1", "https://example.test/emails", "http://api.resend.com/emails"} {
		req := httptest.NewRequest("POST", target, strings.NewReader(`{}`))
		if _, err := capture.RoundTrip(req); err == nil {
			t.Fatal("unexpected mail destination accepted")
		}
	}
}
