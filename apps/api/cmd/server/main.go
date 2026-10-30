package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tesserix/onboarding-service/api/internal/api"
	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/config"
	"github.com/tesserix/onboarding-service/api/internal/hospitalclaims"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/workloadidentity"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"net/url"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	poolCfg.MaxConns = int32(cfg.PoolSize())
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	st := store.New(pool)
	tokens, err := zitadelTokens(cfg, log)
	if err != nil {
		return err
	}
	client := zitadel.New(cfg.ZitadelIssuer, tokens, cfg.PlatformOrgID, log)

	platformOrgID := cfg.PlatformOrgID
	if platformOrgID == "" {
		if platformOrgID, err = client.ResolveOrgByName(ctx, cfg.PlatformOrgName); err != nil {
			return err
		}
		// Every management call is scoped by this id, so it is worth one log line.
		log.Info("resolved platform organization", "name", cfg.PlatformOrgName, "id", platformOrgID)
		client = zitadel.New(cfg.ZitadelIssuer, tokens, platformOrgID, log)
	}

	identity := onboarding.ZitadelIdentity{Client: client}
	serviceOptions := []onboarding.Option{onboarding.WithRegistryProjectID(cfg.RegistryProjectID)}
	if cfg.StaffIdentityProductKey != "" {
		serviceOptions = append(serviceOptions, onboarding.WithStaffIdentity(cfg.StaffIdentityProductKey, client))
	}
	svc := onboarding.New(st, identity, authz.New(st), log, serviceOptions...)

	var hospitalWorkloads api.WorkloadVerifier
	if os.Getenv("HOSPITAL_PROVISIONING_ENABLED") == "true" {
		kubeConfig, err := rest.InClusterConfig()
		if err != nil {
			return err
		}
		kube, err := dynamic.NewForConfig(kubeConfig)
		if err != nil {
			return err
		}
		hospitalWorkloads, err = workloadidentity.New(kubeConfig, "onboarding-provisioning", "system:serviceaccount:identity-operator:zitadel-operator")
		if err != nil {
			return err
		}
		target := os.Getenv("HELIVANTA_PROVISIONING_URL")
		u, err := url.Parse(target)
		if err != nil || u.Scheme != "https" || u.Host != "helivanta.app" || u.Path != "/api/internal/v1/hospital-enrolments" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return errors.New("invalid Helivanta provisioning destination")
		}
		serviceOptions = append(serviceOptions, onboarding.WithHospitals(onboarding.HospitalConfig{Claims: hospitalclaims.New(kube, "identity-operator"), Product: "helivanta", HelivantaURL: target, HelivantaTokenFile: "/var/run/helivanta-provisioning/token"}))
		svc = onboarding.New(st, identity, authz.New(st), log, serviceOptions...)
	}

	verifier, err := auth.NewJWKSVerifier(ctx, cfg.ZitadelIssuer, cfg.ZitadelJWKSURL, cfg.Audience, http.DefaultClient)
	if err != nil {
		return err
	}
	registryVerifier, err := auth.NewJWKSVerifier(ctx, cfg.ZitadelIssuer, cfg.ZitadelJWKSURL, cfg.RegistryTokenAudience, http.DefaultClient)
	if err != nil {
		return err
	}
	var legacyTokens api.TokenVerifier
	var legacyOIDC *auth.OIDCClient
	var sessions *auth.SessionCodec
	if cfg.LegacyConsoleEnabled {
		v, e := auth.NewJWKSVerifier(ctx, auth.GoogleIssuer, auth.GoogleJWKSURL, cfg.GoogleClientID, http.DefaultClient)
		if e != nil {
			return e
		}
		legacyTokens = v
		legacyOIDC = auth.NewGoogleClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.RedirectURI)
		sessions, e = auth.NewSessionCodec(cfg.SessionKey)
		if e != nil {
			return e
		}
	}

	var consoleAccess api.TokenVerifier
	if cfg.ConsoleProjectID != "" {
		v, err := auth.NewJWKSVerifier(ctx, cfg.ZitadelIssuer, cfg.ZitadelJWKSURL, cfg.ConsoleProjectID, http.DefaultClient)
		if err != nil {
			return err
		}
		consoleAccess = v.ForConsole(cfg.ConsoleClientID, platformOrgID)
	}

	gin.SetMode(gin.ReleaseMode)
	var mcpTokens api.TokenVerifier
	if cfg.MCPGatewayProjectID != "" {
		v, e := auth.NewJWKSVerifier(ctx, cfg.ZitadelIssuer, cfg.ZitadelJWKSURL, cfg.MCPGatewayProjectID, &http.Client{Timeout: 10 * time.Second})
		if e != nil {
			return e
		}
		mcpTokens = v.ForMCP(cfg.Audience, platformOrgID)
	}
	router := api.NewRouter(api.Deps{
		MCPTokens: mcpTokens,
		Service:   svc, Store: st, Tokens: verifier, RegistryTokens: registryVerifier, HospitalWorkloads: hospitalWorkloads,
		ConsoleTokens: legacyTokens, Sessions: sessions,
		ConsoleAccessTokens:  consoleAccess,
		DisableLegacyConsole: !cfg.LegacyConsoleEnabled,
		OIDC:                 legacyOIDC,
		Allowlist:            auth.ParseAllowlist(cfg.PlatformAdminEmails),
		PlatformOrgID:        platformOrgID, ConsoleURL: cfg.ConsoleURL,
		SecureCookies: cfg.SecureCookies, Log: log,
	})

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	go purgeIdempotencyKeys(ctx, st, log)
	if cfg.StaffIdentityProductKey != "" {
		go purgeStaffIdentityHistoryCursors(ctx, st, log)
	}
	go reapRegistryCredentials(ctx, svc, log)

	errs := make(chan error, 1)
	go func() {
		log.Info("listening", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errs <- err
		}
	}()

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}

	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownGrace)
	defer cancel()
	return srv.Shutdown(shutdown)
}

func reapRegistryCredentials(ctx context.Context, svc *onboarding.Service, log *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := svc.ReapRegistryCredentials(ctx, 100); err != nil && !errors.Is(err, context.Canceled) {
			log.Warn("could not reap Registry credentials", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// The machine key is the credential this service is meant to run on; the PAT is
// break-glass only, since it is instance-wide and cannot expire.
func zitadelTokens(cfg config.Config, log *slog.Logger) (zitadel.TokenSource, error) {
	if cfg.ZitadelMachineKey == "" {
		log.Warn("using a personal access token for Zitadel; this credential is instance-wide")
		return zitadel.StaticToken(cfg.ZitadelPAT), nil
	}
	tokenURL := strings.TrimSuffix(cfg.ZitadelIssuer, "/") + "/oauth/v2/token"
	return zitadel.NewKeyTokenSource([]byte(cfg.ZitadelMachineKey), cfg.ZitadelIssuer, tokenURL, log)
}

// Idempotency keys are a replay window, not a record; the ledger is the record.
func purgeIdempotencyKeys(ctx context.Context, st *store.Store, log *slog.Logger) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := st.PurgeExpiredIdempotencyKeys(ctx); err != nil {
				log.Warn("could not purge idempotency keys", "error", err)
			}
		}
	}
}

func purgeStaffIdentityHistoryCursors(ctx context.Context, st *store.Store, log *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, err := st.PurgeExpiredStaffIdentityHistoryCursors(bounded)
			cancel()
			if err != nil {
				log.Warn("could not purge expired Staff history cursors", "error", err)
			}
		}
	}
}
