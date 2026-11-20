package onboarding

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// oneShotNative fakes the four native calls the combined proof makes and
// records their order.
type oneShotNative struct {
	mu                       sync.Mutex
	verified                 bool
	resetStatus, passwordPol bool
	verifyStatus             int
	calls                    []string
	row                      string
	tenant, email            string
}

func (n *oneShotNative) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		n.mu.Lock()
		defer n.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/users/" + n.row:
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": n.row, "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": "helivanta-" + n.tenant}, "human": map[string]any{"email": map[string]any{"email": n.email, "isVerified": n.verified}}}})
		case "/v2/users/" + n.row + "/password_reset":
			n.calls = append(n.calls, "reset")
			if n.resetStatus {
				w.WriteHeader(503)
				return
			}
			_, _ = io.WriteString(w, `{"verificationCode":"reset-1"}`)
		case "/v2/users/" + n.row + "/password":
			n.calls = append(n.calls, "password")
			var body struct {
				Code string `json:"verificationCode"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Code != "reset-1" {
				t.Errorf("password set with %q, not the reset code fetched in this request", body.Code)
			}
			if n.passwordPol {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"code":3,"message":"Errors.User.PasswordComplexityPolicy.MinLength (DOMAIN-HuJf6)","details":[{"@type":"type.googleapis.com/zitadel.v1.ErrorDetail","id":"DOMAIN-HuJf6"}]}`)
				return
			}
			_, _ = io.WriteString(w, `{}`)
		case "/v2/users/" + n.row + "/email/verify":
			n.calls = append(n.calls, "verify")
			if n.verifyStatus != 0 {
				w.WriteHeader(n.verifyStatus)
				_, _ = io.WriteString(w, `{"code":3,"message":"Errors.User.Code.Invalid (CODE-woT0xc)","details":[{"@type":"type.googleapis.com/zitadel.v1.ErrorDetail","id":"CODE-woT0xc"}]}`)
				return
			}
			n.verified = true
			_, _ = io.WriteString(w, `{}`)
		default:
			t.Errorf("unexpected identity call %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}
}

func (n *oneShotNative) order() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.calls...)
}

func TestOneShotProofSetsThePasswordFirstThenVerifiesTheEmail(t *testing.T) {
	svc, _, st := newService(t)
	seedProduct(t, st, "helivanta")
	row := newCustomerMailRow(t, svc, st)
	native := &oneShotNative{row: row.OwnerSubject, tenant: row.TenantID.String(), email: row.Input.Owner.Email}
	server := httptest.NewServer(native.handler(t))
	t.Cleanup(server.Close)
	svc.signupIdentity = zitadel.New(server.URL, zitadel.StaticToken("synthetic-token"), "platform", nil)
	next := sha256.Sum256([]byte("next"))
	proof := customercontract.Proof{UserID: row.OwnerSubject, Code: "email-code", Password: "Long-enough-pass-1!"}

	id, receipt, err := svc.ProveCustomer(t.Context(), proof, next[:], false)
	if err != nil || id != row.ID {
		t.Fatalf("one-shot proof failed: %v", err)
	}
	if got := native.order(); len(got) != 3 || got[0] != "reset" || got[1] != "password" || got[2] != "verify" {
		t.Fatalf("native order %v, want reset, password, verify", got)
	}
	if receipt.State == customercontract.PasswordActionRequired || receipt.State == customercontract.EmailActionRequired {
		t.Fatalf("receipt still asks for a step: %s", receipt.State)
	}
	after, err := st.CustomerSignup(t.Context(), row.ID, next[:])
	if err != nil || !after.ReceiptVerified || !after.PasswordConfigured {
		t.Fatalf("receipt not rotated as verified and configured: %+v %v", after, err)
	}
}

func TestOneShotProofPolicyRefusalConsumesNothing(t *testing.T) {
	svc, _, st := newService(t)
	seedProduct(t, st, "helivanta")
	row := newCustomerMailRow(t, svc, st)
	native := &oneShotNative{row: row.OwnerSubject, tenant: row.TenantID.String(), email: row.Input.Owner.Email, passwordPol: true}
	server := httptest.NewServer(native.handler(t))
	t.Cleanup(server.Close)
	svc.signupIdentity = zitadel.New(server.URL, zitadel.StaticToken("synthetic-token"), "platform", nil)
	next := sha256.Sum256([]byte("next"))

	_, _, err := svc.ProveCustomer(t.Context(), customercontract.Proof{UserID: row.OwnerSubject, Code: "email-code", Password: "short"}, next[:], false)
	if !errors.Is(err, ErrPasswordPolicy) {
		t.Fatalf("policy refusal not surfaced: %v", err)
	}
	for _, call := range native.order() {
		if call == "verify" {
			t.Fatal("email code was spent on a refused password")
		}
	}
	after, err := st.CustomerSignup(t.Context(), row.ID, row.ReceiptHash)
	if err != nil || after.ReceiptVerified || after.PasswordConfigured {
		t.Fatalf("a refused password changed the receipt: %+v %v", after, err)
	}

	// Same link, acceptable password: completes.
	native.passwordPol = false
	if _, _, err = svc.ProveCustomer(t.Context(), customercontract.Proof{UserID: row.OwnerSubject, Code: "email-code", Password: "Long-enough-pass-1!"}, next[:], false); err != nil {
		t.Fatalf("retry after policy refusal failed: %v", err)
	}
}

func TestOneShotProofExpiredEmailCodeKeepsTheSetPasswordRecorded(t *testing.T) {
	svc, _, st := newService(t)
	seedProduct(t, st, "helivanta")
	row := newCustomerMailRow(t, svc, st)
	// The worker has asked for the identity: an unverified receipt in this
	// state reads email_action_required, which is what must survive.
	leased, err := st.LeaseCustomerSignups(t.Context(), 1, time.Now().UTC())
	if err != nil || len(leased) != 1 || leased[0].ID != row.ID {
		t.Fatalf("lease the seeded signup: %v %v", leased, err)
	}
	if err = st.FinishCustomerSignup(t.Context(), leased[0], "identity_requested", "", time.Now().UTC().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	native := &oneShotNative{row: row.OwnerSubject, tenant: row.TenantID.String(), email: row.Input.Owner.Email, verifyStatus: 400}
	server := httptest.NewServer(native.handler(t))
	t.Cleanup(server.Close)
	svc.signupIdentity = zitadel.New(server.URL, zitadel.StaticToken("synthetic-token"), "platform", nil)
	next := sha256.Sum256([]byte("next"))

	_, _, err = svc.ProveCustomer(t.Context(), customercontract.Proof{UserID: row.OwnerSubject, Code: "stale", Password: "Long-enough-pass-1!"}, next[:], false)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired email code not refused: %v", err)
	}
	after, err := st.CustomerSignup(t.Context(), row.ID, row.ReceiptHash)
	if err != nil || after.ReceiptVerified || !after.PasswordConfigured {
		t.Fatalf("set password not recorded, or receipt wrongly verified: %+v %v", after, err)
	}
	if customerReceipt(after).State != customercontract.EmailActionRequired {
		t.Fatalf("receipt should still ask for the email, got %s", customerReceipt(after).State)
	}
}

func TestOneShotProofResetCodeOutageTouchesNothing(t *testing.T) {
	svc, _, st := newService(t)
	seedProduct(t, st, "helivanta")
	row := newCustomerMailRow(t, svc, st)
	native := &oneShotNative{row: row.OwnerSubject, tenant: row.TenantID.String(), email: row.Input.Owner.Email, resetStatus: true}
	server := httptest.NewServer(native.handler(t))
	t.Cleanup(server.Close)
	svc.signupIdentity = zitadel.New(server.URL, zitadel.StaticToken("synthetic-token"), "platform", nil)
	next := sha256.Sum256([]byte("next"))

	_, _, err := svc.ProveCustomer(t.Context(), customercontract.Proof{UserID: row.OwnerSubject, Code: "email-code", Password: "Long-enough-pass-1!"}, next[:], false)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("reset-code outage not unavailable: %v", err)
	}
	if got := native.order(); len(got) != 1 || got[0] != "reset" {
		t.Fatalf("outage still reached %v", got)
	}
}
