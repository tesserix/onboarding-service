//go:build identityfixture

// The fixture binary is excluded from production builds and serves only reads.
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/tesserix/onboarding-service/api/internal/api"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

const localIssuer = "http://auth.tesserix.localhost:20080"
const fixtureCredential = "Bearer helivanta-local-identity-fixture-only"

func validateConfig(enabled, issuer, dsn string) error {
	u, err := url.Parse(dsn)
	if err != nil || enabled != "true" || issuer != localIssuer || u.Scheme != "postgres" || u.Hostname() != "127.0.0.1" || u.Path != "/onboarding_fixture" || u.RawQuery != "sslmode=disable" || u.Fragment != "" || u.User == nil || u.User.Username() != "helivanta" {
		return errors.New("identity fixture requires explicit local configuration")
	}
	password, ok := u.User.Password()
	port, e := strconv.Atoi(u.Port())
	if !ok || password != "helivanta" || e != nil || port < 1 || port > 65535 {
		return errors.New("identity fixture requires local development database")
	}
	return nil
}

type fixtureManifest struct {
	ProjectID  string `json:"projectId"`
	AdminOrgID string `json:"adminOrgId"`
}
type fixtureVerifier struct{}

func (fixtureVerifier) Verify(ctx context.Context, credential string) error {
	if ctx.Err() != nil || subtle.ConstantTimeCompare([]byte(credential), []byte(fixtureCredential)) != 1 {
		return errors.New("fixture workload denied")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		slog.Error("local identity fixture stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	manifestPath := flag.String("manifest", "dev/zitadel/secrets/identity-fixture.json", "local public identity manifest")
	patPath := flag.String("pat-file", "dev/zitadel/secrets/helivanta-seed.pat", "local native seed credential file")
	dsn := flag.String("dsn", "postgres://helivanta:helivanta@127.0.0.1:5432/onboarding_fixture?sslmode=disable", "isolated local fixture database")
	flag.Parse()
	if err := validateConfig(os.Getenv("HELIVANTA_IDENTITY_FIXTURE"), os.Getenv("ZITADEL_ISSUER_URL"), *dsn); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	setup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	file, err := os.Open(*manifestPath)
	if err != nil {
		return errors.New("local fixture manifest unavailable")
	}
	defer func() { _ = file.Close() }()
	var manifest fixtureManifest
	decoder := json.NewDecoder(io.LimitReader(file, 4097))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&manifest) != nil || decoder.Decode(new(any)) != io.EOF || manifest.ProjectID == "" || manifest.AdminOrgID == "" {
		return errors.New("invalid local fixture manifest")
	}
	raw, err := os.ReadFile(*patPath)
	if err != nil || len(raw) == 0 || len(raw) > 8192 {
		return errors.New("local fixture native credential unavailable")
	}
	native := zitadel.New(localIssuer, zitadel.StaticToken(strings.TrimSpace(string(raw))), manifest.AdminOrgID, nil)
	pool, err := pgxpool.New(setup, *dsn)
	if err != nil {
		return errors.New("local fixture database unavailable")
	}
	defer pool.Close()
	st := store.New(pool)
	product, err := st.Product(setup, "helivanta")
	if errors.Is(err, store.ErrNotFound) {
		err = st.CreateProduct(setup, store.Product{Key: "helivanta", DisplayName: "Helivanta local identity fixture", ZitadelProjectID: manifest.ProjectID, MachineUserID: "local-fixture", Identity: store.IdentityZitadel, TenantSecrets: store.TenantSecretsNone, Tenancy: store.TenancyOrg, DataResidency: "in"})
	} else if err == nil && (product.ZitadelProjectID != manifest.ProjectID || product.MachineUserID != "local-fixture") {
		return errors.New("local fixture product mismatch")
	}
	if err != nil {
		return errors.New("local fixture product unavailable")
	}
	for _, id := range []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"} {
		org := "helivanta-" + id
		proof, e := native.VerifyOrganization(setup, org, id, manifest.ProjectID)
		if e != nil || proof.State != "active" {
			return fmt.Errorf("local fixture native tenant proof unavailable: %s", id)
		}
		tenant := uuid.MustParse(id)
		retained, e := st.Organization(setup, tenant)
		if errors.Is(e, store.ErrNotFound) {
			_, e = st.CreateOrganization(setup, store.Organization{ID: tenant, ZitadelOrgID: org, Name: "Synthetic local fixture", CreatedByProduct: "helivanta"})
		} else if e == nil && (retained.ZitadelOrgID != org || retained.CreatedByProduct != "helivanta" || retained.State != "active") {
			return errors.New("local fixture tenant mismatch")
		}
		if e != nil {
			return errors.New("local fixture tenant unavailable")
		}
		if e = st.Grant(setup, tenant, "helivanta", nil); e != nil {
			return errors.New("local fixture enrollment unavailable")
		}
	}
	service := onboarding.New(st, onboarding.ZitadelIdentity{Client: native}, authz.New(st), nil, onboarding.WithCustomerIdentity(native))
	router := api.NewRouter(api.Deps{Service: service, Store: st, CustomerWorkloads: fixtureVerifier{}, DisableLegacyConsole: true})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method == http.MethodPost && (r.URL.Path == "/internal/v1/customer-signups/identity-projection" || r.URL.Path == "/internal/v1/customer-signups/login-target")) || (r.Method == http.MethodGet && r.URL.Path == "/readyz") {
			router.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
	server := &http.Server{Addr: "127.0.0.1:18787", Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 30 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case e := <-done:
		if errors.Is(e, http.ErrServerClosed) {
			return nil
		}
		return errors.New("local fixture listener unavailable")
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}
