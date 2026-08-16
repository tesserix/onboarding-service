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
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
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
	svc := onboarding.New(st, identity, authz.New(st), log)

	verifier, err := auth.NewJWKSVerifier(ctx, cfg.ZitadelIssuer, cfg.ZitadelJWKSURL, cfg.Audience, http.DefaultClient)
	if err != nil {
		return err
	}
	sessions, err := auth.NewSessionCodec(cfg.SessionKey)
	if err != nil {
		return err
	}

	gin.SetMode(gin.ReleaseMode)
	router := api.NewRouter(api.Deps{
		Service: svc, Store: st, Tokens: verifier, Sessions: sessions,
		OIDC: &auth.OIDCClient{
			Issuer: cfg.ZitadelIssuer, ClientID: cfg.ConsoleClientID, RedirectURI: cfg.RedirectURI,
		},
		PlatformOrgID: platformOrgID, ConsoleURL: cfg.ConsoleURL,
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
