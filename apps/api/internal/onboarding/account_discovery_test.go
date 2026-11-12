package onboarding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/signupmail"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAccountDiscoveryRequiresProofAndRechecksCurrentCustomerEligibility(t *testing.T) {
	svc, idp, st := newService(t)
	seedProduct(t, st, "helivanta")
	svc.idp = securityIdentity{idp}
	tenants := []uuid.UUID{uuid.New(), uuid.New()}
	names := []string{"Hospital", strings.Repeat("ଅ", 100)}
	users := []any{}
	for i, tenant := range tenants {
		org := "helivanta-" + tenant.String()
		if _, err := st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: org, Name: names[i], CreatedByProduct: "helivanta"}); err != nil {
			t.Fatal(err)
		}
		if err := st.Grant(t.Context(), tenant, "helivanta", []string{"helivanta-user"}); err != nil {
			t.Fatal(err)
		}
		users = append(users, map[string]any{"userId": "staff-" + tenant.String(), "state": "USER_STATE_ACTIVE", "details": map[string]string{"resourceOwner": org}, "human": map[string]any{"email": map[string]any{"email": "person@example.test", "isVerified": true}}})
	}
	var reads atomic.Int32
	native := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"details": map[string]string{"totalResult": "2"}, "result": users})
	}))
	defer native.Close()
	svc.signupIdentity = zitadel.New(native.URL, zitadel.StaticToken("synthetic"), "platform", nil)
	key := t.TempDir() + "/key"
	if os.WriteFile(key, []byte("re_synthetic"), 0600) != nil {
		t.Fatal("key fixture")
	}
	var code string
	var err error
	svc.signupMail, err = signupmail.New(key, "onboarding@helivanta.app", mailTransport(func(r *http.Request) (*http.Response, error) {
		var body map[string]any
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			t.Fatal("mail payload")
		}
		code = regexp.MustCompile(`[0-9]{8}`).FindString(body["text"].(string))
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":"synthetic-message"}`))}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	browser := sha256.Sum256([]byte("browser"))
	source := sha256.Sum256([]byte(uuid.NewString()))
	id, _ := uuid.NewV7()
	in := customercontract.DiscoveryStart{ID: id.String(), Email: "person@example.test", BrowserHash: hex.EncodeToString(browser[:]), SourceHash: hex.EncodeToString(source[:])}
	access := customercontract.DiscoveryAccess{ID: in.ID, BrowserHash: in.BrowserHash}
	if err = svc.StartAccountDiscovery(t.Context(), in); err != nil || code == "" {
		t.Fatal("challenge unavailable", err)
	}
	if _, err = svc.AccountChoices(t.Context(), access); !errors.Is(err, ErrNotFound) || reads.Load() != 0 {
		t.Fatal("pre-proof discovery reached native identity", err)
	}
	proof := customercontract.DiscoveryProof{ID: in.ID, BrowserHash: in.BrowserHash, Code: code}
	if err = svc.VerifyAccountDiscovery(t.Context(), proof); err != nil {
		t.Fatal(err)
	}
	if err = svc.VerifyAccountDiscovery(t.Context(), proof); !errors.Is(err, ErrNotFound) {
		t.Fatal("proof replay accepted")
	}
	choices, err := svc.AccountChoices(t.Context(), access)
	if err != nil || len(choices.Accounts) != 2 || choices.Accounts[0].Name != "Hospital" || choices.Accounts[1].Name != names[1] {
		t.Fatal("named hospital/pharmacy choices missing", err)
	}
	if err = st.RevokeGrant(t.Context(), tenants[0], "helivanta"); err != nil {
		t.Fatal(err)
	}
	choices, err = svc.AccountChoices(t.Context(), access)
	if err != nil || len(choices.Accounts) != 1 || choices.Accounts[0].TenantID != tenants[1].String() {
		t.Fatal("revoked customer remained eligible", err)
	}
}
