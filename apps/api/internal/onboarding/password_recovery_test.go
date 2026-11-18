package onboarding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/signupmail"
)

func TestPasswordRecoveryChallengeSendsWithoutAccountLookupAndStopsAfterProof(t *testing.T) {
	svc, _, _ := newService(t)
	// No native identity client or enrolled customer: delivery must not depend on account existence.
	svc.signupIdentity = nil
	svc.idp = nil
	key := t.TempDir() + "/key"
	if err := os.WriteFile(key, []byte("re_synthetic"), 0600); err != nil {
		t.Fatal(err)
	}
	var code string
	calls := 0
	client, err := signupmail.New(key, "onboarding@helivanta.com", mailTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		code = regexp.MustCompile(`[0-9]{8}`).FindString(body["text"].(string))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"synthetic-message"}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	svc.signupMail = client
	hash := sha256.Sum256([]byte("browser"))
	source := sha256.Sum256([]byte(uuid.NewString()))
	in := customercontract.PasswordRecoveryStart{ID: uuid.Must(uuid.NewV7()).String(), TenantID: uuid.NewString(), Email: "Person@example.test", BrowserHash: hex.EncodeToString(hash[:]), SourceHash: hex.EncodeToString(source[:])}
	if err := svc.StartPasswordRecovery(t.Context(), in); err != nil || len(code) != 8 || calls != 1 {
		t.Fatal("challenge delivery", err)
	}
	if err := svc.StartPasswordRecovery(t.Context(), in); err != nil || calls != 1 {
		t.Fatal("command replay dispatched mail", err)
	}
	proof := customercontract.PasswordRecoveryProof{ID: in.ID, BrowserHash: in.BrowserHash, Code: code}
	if err := svc.VerifyPasswordRecovery(t.Context(), proof); err != nil {
		t.Fatal("mailbox proof", err)
	}
	if err := svc.VerifyPasswordRecovery(t.Context(), proof); !errors.Is(err, ErrNotFound) {
		t.Fatal("proof replay admitted", err)
	}
	in.ID = uuid.Must(uuid.NewV7()).String()
	if err := svc.StartPasswordRecovery(t.Context(), in); err != nil || calls != 1 {
		t.Fatal("verified challenge sent more mail", err)
	}
}

func TestPasswordRecoverySeparatesMailboxPurposeAndRejectsInvalidInput(t *testing.T) {
	browser := strings.Repeat("a", 64)
	id := uuid.Must(uuid.NewV7()).String()
	code := "12345678"
	if string(passwordRecoveryCodeHash(id, browser, code)) == string(discoveryCodeHash(browser, code)) || string(passwordRecoveryCodeHash(id, browser, code)) == string(passwordRecoveryCodeHash(uuid.Must(uuid.NewV7()).String(), browser, code)) {
		t.Fatal("proof not bound to purpose and command")
	}
	svc := &Service{}
	if err := svc.StartPasswordRecovery(t.Context(), customercontract.PasswordRecoveryStart{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid start admitted", err)
	}
	if err := svc.VerifyPasswordRecovery(t.Context(), customercontract.PasswordRecoveryProof{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid proof admitted", err)
	}
	in := customercontract.PasswordRecoveryStart{ID: id, TenantID: uuid.NewString(), BrowserHash: browser, SourceHash: browser, Email: "person@example.test"}
	if err := svc.StartPasswordRecovery(t.Context(), in); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing persistence admitted", err)
	}
	if err := svc.VerifyPasswordRecovery(t.Context(), customercontract.PasswordRecoveryProof{ID: id, BrowserHash: browser, Code: code}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing proof persistence admitted", err)
	}
}
