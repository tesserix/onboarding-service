// Package staffclienttest exercises the real identity owner with isolated PostgreSQL
// schemas and simulated external Zitadel and email providers. Production does not import it.
package staffclienttest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tesserix/onboarding-service/api/internal/api"
	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/signupmail"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"github.com/tesserix/onboarding-service/api/staffclient"
	"github.com/tesserix/onboarding-service/api/staffcontract"
)

// WorkloadToken is accepted only by the isolated integration-test router.
const WorkloadToken = "staff-owner-fixture-only"
const nativeCode = "TEST-native-proof"

type Fixture struct {
	Client    *staffclient.Client
	Router    http.Handler
	Creates   atomic.Int32
	Mails     atomic.Int32
	native    *nativeProvider
	mu        sync.Mutex
	delivered staffcontract.StaffRecipientInput
}

func (*Fixture) String() string   { return "[redacted staff owner fixture]" }
func (*Fixture) GoString() string { return "[redacted staff owner fixture]" }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type tokens struct{}

func (tokens) Token(context.Context) (string, error) { return WorkloadToken, nil }
func (tokens) Verify(_ context.Context, raw string) (auth.Claims, error) {
	if raw != WorkloadToken {
		return auth.Claims{}, auth.ErrInvalidToken
	}
	return auth.Claims{Subject: "TEST-helivanta-machine"}, nil
}

type identity struct{ onboarding.Identity }

func (identity) VerifyOrganization(_ context.Context, org, tenant, project string) (zitadel.OrganizationIdentity, error) {
	return zitadel.OrganizationIdentity{SchemaVersion: 1, TenantID: tenant, OrganizationID: org, ProjectID: project, GrantID: "TEST-grant", State: "active", VerifiedAt: time.Now().UTC(), SourceRevisions: zitadel.IdentityRevisions{Organization: "101", Metadata: "102", Project: "103", Grant: "104"}}, nil
}

// New requires an explicitly isolated loopback test database and the checked
// canonical SQL file. It never connects to a product database or starts a database.
func New(t testing.TB, dsn, schemaFile string, tenants ...uuid.UUID) *Fixture {
	t.Helper()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil || cfg.ConnConfig.Host != "127.0.0.1" || (cfg.ConnConfig.Database != "app_test" && cfg.ConnConfig.Database != "onboarding_fixture") {
		t.Fatal("staff owner fixture requires an isolated loopback test database")
	}
	owner, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal("staff owner fixture database unavailable")
	}
	t.Cleanup(owner.Close)
	schema := "staff_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err = owner.Exec(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal("staff owner fixture schema unavailable")
	}
	t.Cleanup(func() {
		_, err := owner.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		if err != nil {
			t.Error("staff owner fixture cleanup failed")
		}
	})
	command := exec.CommandContext(t.Context(), "psql", "-h", cfg.ConnConfig.Host, "-p", strconv.Itoa(int(cfg.ConnConfig.Port)), "-U", cfg.ConnConfig.User, "-d", cfg.ConnConfig.Database, "-v", "ON_ERROR_STOP=1", "-c", "SET search_path TO "+schema, "-f", schemaFile)
	command.Env = append(os.Environ(), "PGPASSWORD="+cfg.ConnConfig.Password)
	if command.Run() != nil {
		t.Fatal("staff owner fixture canonical schema failed")
	}
	if _, err = owner.Exec(t.Context(), "GRANT USAGE ON SCHEMA "+schema+" TO onboarding"); err != nil {
		t.Fatal("staff owner fixture app schema unavailable")
	}
	appCfg := cfg.Copy()
	appCfg.ConnConfig.RuntimeParams["search_path"] = schema
	appCfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE onboarding")
		return err
	}
	pool, err := pgxpool.NewWithConfig(t.Context(), appCfg)
	if err != nil {
		t.Fatal("staff owner fixture app pool unavailable")
	}
	t.Cleanup(pool.Close)
	st := store.New(pool)
	if err = st.CreateProduct(t.Context(), store.Product{Key: "helivanta", DisplayName: "Synthetic Helivanta", ZitadelProjectID: "TEST-project", MachineUserID: "TEST-helivanta-machine", Identity: store.IdentityZitadel, TenantSecrets: store.TenantSecretsNone, Tenancy: store.TenancyOrg, DataResidency: "in"}); err != nil {
		t.Fatal("fixture product unavailable")
	}
	for _, tenant := range tenants {
		if _, err = st.CreateOrganization(t.Context(), store.Organization{ID: tenant, ZitadelOrgID: "test-org-" + tenant.String(), Name: "Synthetic fixture tenant", CreatedByProduct: "helivanta"}); err != nil {
			t.Fatal("fixture tenant unavailable")
		}
		if err = st.Grant(t.Context(), tenant, "helivanta", nil); err != nil {
			t.Fatal("fixture enrollment unavailable")
		}
	}
	f := &Fixture{}
	f.native = &nativeProvider{fixture: f, users: make(map[string]*nativeUser)}
	server := httptest.NewServer(http.HandlerFunc(f.native.serve))
	t.Cleanup(server.Close)
	keyFile := t.TempDir() + "/email-key"
	if os.WriteFile(keyFile, []byte("re_fixture_only"), 0600) != nil {
		t.Fatal("fixture mail credential unavailable")
	}
	mailer, err := signupmail.New(keyFile, "onboarding@helivanta.app", roundTrip(f.deliver))
	if err != nil {
		t.Fatal("fixture mailer unavailable")
	}
	native := zitadel.New(server.URL, zitadel.StaticToken("TEST-native-token"), "TEST-platform", nil)
	svc := onboarding.New(st, identity{}, authz.New(st), nil, onboarding.WithStaffIdentity("helivanta", f.native), onboarding.WithCustomerIdentity(native), onboarding.WithCustomerMail(mailer))
	f.Router = api.NewRouter(api.Deps{Service: svc, Store: st, Tokens: tokens{}, CustomerWorkloads: workload{}, DisableLegacyConsole: true})
	f.Client, err = staffclient.New(staffclient.Config{Endpoint: "http://onboarding-api.onboarding.svc.cluster.local:8080", ProductKey: "helivanta", Tokens: tokens{}, HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		w := httptest.NewRecorder()
		f.Router.ServeHTTP(w, r)
		return w.Result(), nil
	})}})
	if err != nil {
		t.Fatal("fixture public staff client unavailable")
	}
	return f
}

func (f *Fixture) Invitation() staffcontract.StaffRecipientInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.delivered
}

func (f *Fixture) deliver(r *http.Request) (*http.Response, error) {
	var message struct{ Text string }
	if json.NewDecoder(r.Body).Decode(&message) != nil {
		return nil, errors.New("invalid fixture mail")
	}
	var found bool
	for _, line := range strings.Split(message.Text, "\n") {
		if !strings.HasPrefix(line, "https://helivanta.app/staff/accept#") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil {
			return nil, errors.New("invalid fixture link")
		}
		values, err := url.ParseQuery(u.EscapedFragment())
		if err != nil {
			return nil, errors.New("invalid fixture fragment")
		}
		id, err := uuid.Parse(values.Get("invitation"))
		if err != nil {
			return nil, errors.New("invalid fixture invitation")
		}
		f.mu.Lock()
		f.delivered = staffcontract.StaffRecipientInput{InvitationID: id, Ticket: values.Get("ticket"), Code: values.Get("code")}
		f.mu.Unlock()
		found = true
	}
	if !found {
		return nil, errors.New("missing fixture invitation")
	}
	f.Mails.Add(1)
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{\"id\":\"test-accepted\"}"))}, nil
}

type workload struct{}

func (workload) Verify(_ context.Context, value string) error {
	if value != "Bearer "+WorkloadToken {
		return errors.New("fixture workload denied")
	}
	return nil
}

type nativeUser struct {
	input              zitadel.StaffIdentityInput
	born               time.Time
	verified, password bool
}
type nativeProvider struct {
	fixture *Fixture
	mu      sync.Mutex
	users   map[string]*nativeUser
}

func (n *nativeProvider) CreateStaffIdentity(_ context.Context, in zitadel.StaffIdentityInput) (zitadel.StaffIdentityCreated, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if _, ok := n.users[in.UserID]; ok {
		return zitadel.StaffIdentityCreated{}, zitadel.ErrStaffIdentityUncertain
	}
	n.fixture.Creates.Add(1)
	born := time.Now().UTC().Truncate(time.Microsecond)
	n.users[in.UserID] = &nativeUser{input: in, born: born}
	return zitadel.StaffIdentityCreated{ID: in.UserID, CreatedAt: born}, nil
}
func (n *nativeProvider) ObserveStaffIdentity(_ context.Context, in zitadel.StaffIdentityInput, born time.Time) (zitadel.StaffIdentityObservation, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	u, ok := n.users[in.UserID]
	if !ok || u.input != in || !u.born.Equal(born) {
		return zitadel.StaffIdentityObservation{}, zitadel.ErrIdentityUnavailable
	}
	state := "initial"
	if u.verified {
		state = "active"
	}
	return zitadel.StaffIdentityObservation{UserID: in.UserID, OrganizationID: in.OrganizationID, State: state, EmailVerified: u.verified, CreatedAt: u.born, ObservedAt: time.Now().UTC(), UserRevision: "101", MetadataRevision: "102"}, nil
}
func (n *nativeProvider) serve(w http.ResponseWriter, r *http.Request) {
	n.mu.Lock()
	defer n.mu.Unlock()
	pieces := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(pieces) < 4 || pieces[0] != "v2" || pieces[1] != "users" {
		http.NotFound(w, r)
		return
	}
	u, ok := n.users[pieces[2]]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "POST" && (strings.HasSuffix(r.URL.Path, "/email/send") || strings.HasSuffix(r.URL.Path, "/password_reset")):
		_ = json.NewEncoder(w).Encode(map[string]string{"verificationCode": nativeCode})
	case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/authentication_methods"):
		methods := []string{}
		if u.password {
			methods = append(methods, "AUTHENTICATION_METHOD_TYPE_PASSWORD")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"details": map[string]string{"totalResult": strconv.Itoa(len(methods))}, "authMethodTypes": methods})
	case r.Method == "POST" && (strings.HasSuffix(r.URL.Path, "/email/verify") || strings.HasSuffix(r.URL.Path, "/password")):
		var proof map[string]any
		if json.NewDecoder(r.Body).Decode(&proof) != nil || proof["verificationCode"] != nativeCode {
			w.WriteHeader(400)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/email/verify") {
			u.verified = true
		} else if !u.verified {
			w.WriteHeader(400)
			return
		} else {
			u.password = true
		}
		_ = json.NewEncoder(w).Encode(map[string]any{})
	default:
		http.NotFound(w, r)
	}
}
