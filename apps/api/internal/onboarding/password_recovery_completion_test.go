package onboarding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/tesserix/onboarding-service/api/internal/signupmail"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func TestPasswordRecoveryCompletesNativeWriteOnceAndPreservesUncertainOutcomes(t *testing.T) {
	for _, mode := range []string{"completed", "refused", "unconfirmed", "applied", "cancelled", "late_prepare"} {
		t.Run(mode, func(t *testing.T) {
			svc, idp, st := newService(t)
			seedProduct(t, st, "helivanta")
			svc.idp = securityIdentity{idp}
			tenant := uuid.New()
			org := "helivanta-" + tenant.String()
			browser := recoveryCompletionHash("browser")
			source := recoveryCompletionHash(uuid.NewString())
			challenge := uuid.Must(uuid.NewV7())
			if _, err := st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: org, Name: "Recovery hospital", CreatedByProduct: "helivanta"}); err != nil {
				t.Fatal(err)
			}
			if err := st.Grant(t.Context(), tenant, "helivanta", []string{"helivanta-user"}); err != nil {
				t.Fatal(err)
			}
			code := passwordRecoveryCodeHash(challenge.String(), hex.EncodeToString(browser), "12345678")
			if _, err := st.ReservePasswordRecovery(t.Context(), store.PasswordRecoveryReservation{ID: challenge, TenantID: tenant, BrowserHash: browser, SourceHash: source, Email: "person@example.test", CodeHash: code, CreatedAt: time.Now().UTC()}); err != nil {
				t.Fatal(err)
			}
			if err := st.VerifyPasswordRecovery(t.Context(), challenge, browser, code, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			changed := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
			var nativeWrites atomic.Int32
			var applied atomic.Bool
			var readUnavailable atomic.Bool
			native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v2/users":
					_ = json.NewEncoder(w).Encode(map[string]any{"result": []any{map[string]any{"userId": "account", "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": org}, "human": map[string]any{"email": map[string]any{"email": "person@example.test", "isVerified": true}}}}})
				case "/v2/users/account":
					if readUnavailable.Load() {
						w.WriteHeader(503)
						return
					}
					cutoff := "2026-01-01T00:00:00Z"
					if applied.Load() {
						cutoff = changed.Format(time.RFC3339Nano)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": "account", "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": org}, "human": map[string]any{"email": map[string]any{"email": "person@example.test", "isVerified": true}, "passwordChanged": cutoff}}})
				case "/v2/users/account/password_reset":
					_ = json.NewEncoder(w).Encode(map[string]string{"verificationCode": "native-code"})
				case "/v2/users/account/password":
					nativeWrites.Add(1)
					if mode == "refused" {
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"code":3,"details":[{"@type":"type.googleapis.com/zitadel.v1.ErrorDetail","id":"DOMAIN-HuJf6"}]}`))
						return
					}
					if mode == "unconfirmed" {
						w.WriteHeader(503)
						return
					}
					applied.Store(true)
					if mode == "applied" {
						readUnavailable.Store(true)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"details": map[string]string{"resourceOwner": org, "sequence": "42", "changeDate": changed.Format(time.RFC3339Nano)}})
				default:
					t.Errorf("unexpected native path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer native.Close()
			svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)
			// onboarding#51: a real change sends Helivanta's own notice, once per
			// operation; refusals and unconfirmed writes send nothing.
			changedMails, changedKey := 0, ""
			mailKey := t.TempDir() + "/key"
			if err := os.WriteFile(mailKey, []byte("re_synthetic"), 0600); err != nil {
				t.Fatal(err)
			}
			mailer, mailErr := signupmail.New(mailKey, "onboarding@helivanta.app", mailTransport(func(r *http.Request) (*http.Response, error) {
				changedMails++
				changedKey = r.Header.Get("Idempotency-Key")
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"synthetic-message"}`))}, nil
			}))
			if mailErr != nil {
				t.Fatal(mailErr)
			}
			svc.signupMail = mailer
			in := customercontract.PasswordRecoveryPassword{CommandID: uuid.Must(uuid.NewV7()).String(), ID: challenge.String(), BrowserHash: hex.EncodeToString(browser), Password: "synthetic-password"}
			firstCommand := in.CommandID
			if _, err := svc.CompletePasswordRecovery(t.Context(), in); !errors.Is(err, ErrConflict) || nativeWrites.Load() != 0 {
				t.Fatal("password without prepared intent was dispatched", err)
			}
			intent := customercontract.PasswordRecoveryIntent{CommandID: in.CommandID, ID: in.ID, BrowserHash: in.BrowserHash}
			if mode == "late_prepare" {
				cancelled, err := svc.CancelPasswordRecovery(t.Context(), intent)
				if err != nil || cancelled.State != "cancelled" {
					t.Fatal("missing intent cancellation not retained", err)
				}
				prepared, err := svc.PreparePasswordRecovery(t.Context(), intent)
				if err != nil || prepared.State != "cancelled" || nativeWrites.Load() != 0 {
					t.Fatal("late preparation revived cancellation", err)
				}
				if _, err = svc.CompletePasswordRecovery(t.Context(), in); !errors.Is(err, ErrConflict) || nativeWrites.Load() != 0 {
					t.Fatal("late password bypassed cancellation", err)
				}
				return
			}
			prepared, err := svc.PreparePasswordRecovery(t.Context(), intent)
			if err != nil || prepared.State != "prepared" || prepared.CommandID != in.CommandID || nativeWrites.Load() != 0 {
				t.Fatal("preparation changed native credentials", err)
			}
			status, err := svc.ReadPasswordRecovery(t.Context(), customercontract.PasswordRecoveryRead{ID: in.ID, BrowserHash: in.BrowserHash, CommandID: uuid.Must(uuid.NewV7()).String()})
			if !errors.Is(err, ErrNotFound) {
				t.Fatal("unknown exact selector fell back to older receipt", status, err)
			}
			if mode == "cancelled" {
				cancelled, err := svc.CancelPasswordRecovery(t.Context(), intent)
				if err != nil || cancelled.State != "cancelled" {
					t.Fatal("prepared cancellation failed", err)
				}
				if _, err = svc.CompletePasswordRecovery(t.Context(), in); !errors.Is(err, ErrConflict) || nativeWrites.Load() != 0 {
					t.Fatal("cancelled late submit dispatched", err)
				}
				return
			}
			result, err := svc.CompletePasswordRecovery(t.Context(), in)
			if err != nil || result.State != mode || nativeWrites.Load() != 1 {
				t.Fatalf("native completion got %q writes=%d err=%v", result.State, nativeWrites.Load(), err)
			}
			in.Password = "a different retry payload must never dispatch"
			if _, err := svc.CompletePasswordRecovery(t.Context(), in); !errors.Is(err, ErrConflict) || nativeWrites.Load() != 1 {
				t.Fatal("password replay did not require read-only recovery", err)
			}
			replay, err := svc.ReadPasswordRecovery(t.Context(), customercontract.PasswordRecoveryRead{ID: in.ID, BrowserHash: in.BrowserHash, CommandID: in.CommandID})
			if err != nil || replay.State != mode || replay.CommandID != in.CommandID || nativeWrites.Load() != 1 {
				t.Fatal("read-only receipt lost exact outcome", err)
			}
			if mode == "applied" {
				readUnavailable.Store(false)
				replay, err = svc.ReadPasswordRecovery(t.Context(), customercontract.PasswordRecoveryRead{ID: in.ID, BrowserHash: in.BrowserHash, CommandID: in.CommandID})
				if err != nil || replay.State != "completed" || nativeWrites.Load() != 1 {
					t.Fatal("acknowledged native write could not reconcile without another write", err)
				}
			}
			if mode == "unconfirmed" || mode == "completed" {
				in.CommandID = uuid.Must(uuid.NewV7()).String()
				if _, err := svc.CompletePasswordRecovery(t.Context(), in); err == nil || nativeWrites.Load() != 1 {
					t.Fatal("old proof or unresolved account admitted another password write", err)
				}
			}
			wantMails := 0
			if mode == "completed" || mode == "applied" {
				wantMails = 1
			}
			if changedMails != wantMails || (wantMails == 1 && changedKey != "helivanta-password-changed:"+firstCommand) {
				t.Fatalf("password-changed notices: %d (want %d), key %q", changedMails, wantMails, changedKey)
			}
			in.BrowserHash = hex.EncodeToString(recoveryCompletionHash("foreign"))
			if _, err := svc.CompletePasswordRecovery(t.Context(), in); !errors.Is(err, ErrNotFound) {
				t.Fatal("foreign browser completed account recovery", err)
			}
		})
	}
}
func recoveryCompletionHash(value string) []byte { sum := sha256.Sum256([]byte(value)); return sum[:] }
