// Package api is the HTTP boundary: authentication, authorization hand-off,
// validation and error shaping. It holds no business rules.
package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

type Deps struct {
	DisableLegacyConsole bool
	HospitalWorkloads    WorkloadVerifier
	Service              *onboarding.Service
	Store                *store.Store
	// Tokens verifies a product's Zitadel machine token; ConsoleTokens verifies
	// the Google ID token a console human signs in with. Two issuers, two
	// audiences, so they cannot be the same verifier.
	Tokens TokenVerifier
	// RegistryTokens verifies actor tokens delegated by Agent Registry. Its
	// audience is the Registry project, distinct from product onboarding tokens.
	RegistryTokens      TokenVerifier
	ConsoleAccessTokens TokenVerifier
	ConsoleTokens       TokenVerifier
	// Sessions seals the console's cookie; OIDC logs the human in.
	Sessions *auth.SessionCodec
	OIDC     *auth.OIDCClient
	// Allowlist is the set of people who may hold a console session, checked on
	// sign-in and again on every request so a removal takes effect at once.
	Allowlist auth.Allowlist
	// PlatformOrgID is the Zitadel org whose members are platform staff.
	PlatformOrgID string
	ConsoleURL    string
	SecureCookies bool
	Log           *slog.Logger
}

func (d Deps) logger() *slog.Logger {
	if d.Log == nil {
		return slog.Default()
	}
	return d.Log
}

func NewRouter(d Deps) *gin.Engine {
	log := d.logger()
	r := gin.New()
	r.Use(gin.Recovery(), requestID(), securityHeaders(), requestLogger(log))

	r.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })
	r.GET("/readyz", func(c *gin.Context) {
		if err := d.Service.Ready(c.Request.Context()); err != nil {
			abort(c, http.StatusServiceUnavailable, "not ready")
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	})

	if d.OIDC != nil && !d.DisableLegacyConsole {
		d.registerAuthRoutes(r)
	}

	v1 := r.Group("/v1", readBody(), d.authenticate(), newRateLimiter(20, 40).middleware(), d.idempotency())
	d.registerRoutes(v1)
	v1.POST("/hospital-onboardings", d.startHospital)
	v1.GET("/hospital-onboardings/:tenantId", d.getHospital)
	r.POST("/internal/v1/hospital-enrolments", d.completeHospital)
	return r
}
