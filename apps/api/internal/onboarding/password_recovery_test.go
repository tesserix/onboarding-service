package onboarding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/signupmail"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
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
	if _, err := svc.StartPasswordRecovery(t.Context(), in); err != nil || len(code) != 8 || calls != 1 {
		t.Fatal("challenge delivery", err)
	}
	if _, err := svc.StartPasswordRecovery(t.Context(), in); err != nil || calls != 1 {
		t.Fatal("command replay dispatched mail", err)
	}
	proof := customercontract.PasswordRecoveryProof{ID: in.ID, BrowserHash: in.BrowserHash, Code: code}
	if err := svc.VerifyPasswordRecovery(t.Context(), proof); err != nil {
		t.Fatal("mailbox proof", err)
	}
	if err := svc.VerifyPasswordRecovery(t.Context(), proof); !errors.Is(err, ErrNotFound) {
		t.Fatal("proof replay admitted", err)
	}
	originalID := in.ID
	in.ID = uuid.Must(uuid.NewV7()).String()
	if retained, err := svc.StartPasswordRecovery(t.Context(), in); err != nil || calls != 1 || retained.ChallengeID != originalID {
		t.Fatal("verified challenge sent more mail or lost its receipt", err)
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
	if _, err := svc.ReadPasswordRecovery(t.Context(), customercontract.PasswordRecoveryAccess{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid recovery status admitted", err)
	}
	if _, err := svc.ReadPasswordRecovery(t.Context(), customercontract.PasswordRecoveryAccess{ID: id, BrowserHash: browser}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("recovery status missing dependencies admitted", err)
	}
	if _, err := svc.StartPasswordRecovery(t.Context(), customercontract.PasswordRecoveryStart{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid start admitted", err)
	}
	if err := svc.VerifyPasswordRecovery(t.Context(), customercontract.PasswordRecoveryProof{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("invalid proof admitted", err)
	}
	in := customercontract.PasswordRecoveryStart{ID: id, TenantID: uuid.NewString(), BrowserHash: browser, SourceHash: browser, Email: "person@example.test"}
	if _, err := svc.StartPasswordRecovery(t.Context(), in); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing persistence admitted", err)
	}
	if err := svc.VerifyPasswordRecovery(t.Context(), customercontract.PasswordRecoveryProof{ID: id, BrowserHash: browser, Code: code}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing proof persistence admitted", err)
	}
}

func TestPasswordRecoveryStatusRequiresProofAndRechecksTenantEligibility(t *testing.T) {
	svc, idp, st := newService(t)
	seedProduct(t, st, "helivanta")
	svc.idp = securityIdentity{idp}
	tenant := uuid.New()
	org := "helivanta-" + tenant.String()
	if _, err := st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: org, Name: "Recovery hospital", CreatedByProduct: "helivanta"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Grant(t.Context(), tenant, "helivanta", []string{"helivanta-user"}); err != nil {
		t.Fatal(err)
	}
	browser := strings.Repeat("a", 64)
	id := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	raw, _ := hex.DecodeString(browser)
	if _, err := st.ReservePasswordRecovery(t.Context(), store.PasswordRecoveryReservation{ID: id, TenantID: tenant, BrowserHash: raw, SourceHash: raw, Email: "person@example.test", CodeHash: passwordRecoveryCodeHash(id.String(), browser, "12345678"), CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	revokeDuringRead := false
	state := "USER_STATE_LOCKED"
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if revokeDuringRead {
			if err := st.RevokeGrant(t.Context(), tenant, "helivanta"); err != nil {
				t.Error(err)
			}
		}
		if r.URL.Path != "/v2/users" || r.Header.Get("X-Zitadel-Orgid") != org {
			t.Error("wrong native recovery scope")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": []any{map[string]any{"userId": "recovery-account", "state": state, "details": map[string]string{"resourceOwner": org}, "human": map[string]any{"email": map[string]any{"email": "person@example.test", "isVerified": true}}}}})
	}))
	defer native.Close()
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)
	access := customercontract.PasswordRecoveryAccess{ID: id.String(), BrowserHash: browser}
	if _, err := svc.ReadPasswordRecovery(t.Context(), access); !errors.Is(err, ErrNotFound) || calls != 0 {
		t.Fatal("account lookup before mailbox proof", err)
	}
	if err := svc.VerifyPasswordRecovery(t.Context(), customercontract.PasswordRecoveryProof{ID: id.String(), BrowserHash: browser, Code: "12345678"}); err != nil {
		t.Fatal(err)
	}
	view, err := svc.ReadPasswordRecovery(t.Context(), access)
	if err != nil || view.State != "locked" {
		t.Fatal("verified recipient lacks locked guidance", err)
	}
	state = "USER_STATE_ACTIVE"
	view, err = svc.ReadPasswordRecovery(t.Context(), access)
	if err != nil || view.State != "ready" {
		t.Fatal("active account recovery unavailable", err)
	}
	revokeDuringRead = true
	if _, err := svc.ReadPasswordRecovery(t.Context(), access); !errors.Is(err, ErrNotFound) {
		t.Fatal("tenant revoked during native read still disclosed status", err)
	}
	before := calls
	if _, err := svc.ReadPasswordRecovery(t.Context(), access); !errors.Is(err, ErrNotFound) || calls != before {
		t.Fatal("revoked tenant recovery admitted", err)
	}
}
