package onboarding

import (
	"crypto/sha256"
	"encoding/json"
	"github.com/google/uuid"
	identity "github.com/tesserix/devai-sandbox-operator/operators/zitadel/api/v1alpha1"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"io"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/tesserix/onboarding-service/api/internal/signupmail"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

type mailTransport func(*http.Request) (*http.Response, error)

func (f mailTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCustomerInitialMailWaitsForNativeIdentityAndRecordsOnlyProviderAcceptance(t *testing.T) {
	svc, _, st := newService(t)
	seedProduct(t, st, "helivanta")
	row := newCustomerMailRow(t, svc, st)
	userPresent, verified := false, false
	providerReady := false
	codes, sends := 0, 0
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/users":
			_, _ = io.WriteString(w, `{"details":{"timestamp":"2026-11-11T05:36:41.870629Z"}}`)
		case "/v2/users/" + row.OwnerSubject:
			if !userPresent {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]any{"userId": row.OwnerSubject, "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": "helivanta-" + row.TenantID.String()}, "human": map[string]any{"email": map[string]any{"email": row.Input.Owner.Email, "isVerified": verified}}}})
		case "/v2/users/" + row.OwnerSubject + "/email/send":
			codes++
			_, _ = io.WriteString(w, `{"verificationCode":"native-code"}`)
		default:
			t.Errorf("unexpected identity call %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(native.Close)
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic-token"), "platform", nil)
	keyFile := t.TempDir() + "/key"
	if err := os.WriteFile(keyFile, []byte("re_synthetic_key"), 0600); err != nil {
		t.Fatal(err)
	}
	var err error
	svc.signupMail, err = signupmail.New(keyFile, "onboarding@helivanta.app", mailTransport(func(r *http.Request) (*http.Response, error) {
		sends++
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "native-code") {
			t.Error("email missing native proof")
		}
		if !providerReady {
			return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{"error":"provider unavailable"}`))}, nil
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"provider-message"}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	advance := func() {
		t.Helper()
		now = now.Add(time.Minute)
		if err := svc.DeliverCustomers(t.Context(), now); err != nil {
			t.Fatal(err)
		}
	}
	advance()
	claim, err := svc.hospitals.Claims.Get(t.Context(), "helivanta", row.TenantID.String())
	if err != nil || !claim.Spec.ExternalInitialAdminEmail {
		t.Fatal("new claim did not suppress shared SMTP", err)
	}
	advance()
	if codes != 0 || sends != 0 {
		t.Fatal("mail sent before identity existed")
	}
	userPresent = true
	advance()
	failed, err := st.CustomerSignup(t.Context(), row.ID, row.ReceiptHash)
	if err != nil || failed.EmailAcceptedAt != nil || failed.EmailProviderID != "" || failed.ErrorCode != "email_unavailable" || codes != 1 || sends != 1 {
		t.Fatal("provider failure lost retry state", err)
	}
	verified = true
	advance()
	if sends != 1 {
		t.Fatal("verified identity received another verification email")
	}
	verified = false
	providerReady = true
	advance()
	current, err := st.CustomerSignup(t.Context(), row.ID, row.ReceiptHash)
	if err != nil || current.EmailAcceptedAt == nil || current.EmailProviderID != "provider-message" || codes != 2 || sends != 2 {
		t.Fatalf("acceptance missing: codes=%d sends=%d %v", codes, sends, err)
	}
	advance()
	if sends != 2 {
		t.Fatal("accepted first mail sent twice")
	}

}

func newCustomerMailRow(t *testing.T, svc *Service, st *store.Store) store.CustomerSignup {
	t.Helper()
	id := uuid.New()
	hash := sha256.Sum256([]byte(id.String()))
	input := customercontract.Signup{Setup: customercontract.Setup{Name: "TEST Hospital", Kind: customercontract.Hospital, CountryCode: customercontract.India, StateCode: customercontract.Maharashtra}, Owner: &identity.OrganizationInitialAdmin{GivenName: "Test", FamilyName: "Owner", Email: id.String() + "@example.test"}}
	if _, err := svc.StartCustomer(t.Context(), id, hash[:], input); err != nil {
		t.Fatal(err)
	}

	svc.hospitals = &HospitalConfig{Product: "helivanta", Claims: hospitalclaims.New(fake.NewSimpleDynamicClient(runtime.NewScheme()), "identity-operator")}
	row, err := st.CustomerSignup(t.Context(), id, hash[:])
	if err != nil {
		t.Fatal(err)
	}
	return row
}
