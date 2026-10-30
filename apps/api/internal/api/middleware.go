package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"golang.org/x/time/rate"

	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

const (
	requestIDKey  = "requestId"
	principalKey  = "principal"
	sessionKey    = "session"
	bodyKey       = "rawBody"
	sessionCookie = "onboard_session"
	maxBodyBytes  = 256 << 10
)

// requestID gives every response a correlation id, which is the same id the
// ledger records, so an audit line can be traced to a request.
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader("X-Request-Id")
		if id == "" || len(id) > 64 {
			id = uuid.NewString()
		}
		c.Set(requestIDKey, id)
		c.Header("X-Request-Id", id)
		c.Request = c.Request.WithContext(onboarding.WithRequestID(c.Request.Context(), id))
		c.Next()
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
		c.Next()
	}
}

func requestLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		// The path, never the body: request bodies here carry customer emails
		// and IdP secrets.
		log.Info("request",
			"method", c.Request.Method, "path", c.FullPath(), "status", c.Writer.Status(),
			"requestId", c.GetString(requestIDKey), "actor", principalOf(c).Subject,
			"ms", time.Since(started).Milliseconds())
	}
}

// readBody bounds the request and keeps the bytes for the idempotency hash.
func readBody() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil {
			c.Next()
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyBytes+1))
		if err != nil {
			abort(c, http.StatusBadRequest, "could not read the request body")
			return
		}
		if len(body) > maxBodyBytes {
			abort(c, http.StatusRequestEntityTooLarge, "request body is too large")
			return
		}
		c.Set(bodyKey, body)
		c.Request.Body = io.NopCloser(strings.NewReader(string(body)))
		c.Next()
	}
}

type TokenVerifier interface {
	Verify(ctx context.Context, raw string) (auth.Claims, error)
}

// authenticate resolves the caller to a principal. There are exactly three
// kinds, and which one you are is derived from the credential — never from the
// request body or a path parameter.
func (d Deps) authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		if raw := bearer(c); raw != "" {
			claims, err := d.Tokens.Verify(c.Request.Context(), raw)
			if err != nil && d.ConsoleAccessTokens != nil {
				claims, err = d.ConsoleAccessTokens.Verify(c.Request.Context(), raw)
			}
			if err != nil && d.RegistryTokens != nil {
				claims, err = d.RegistryTokens.Verify(c.Request.Context(), raw)
			}
			if err != nil {
				abort(c, http.StatusUnauthorized, "authentication required")
				return
			}
			if claims.Console && (c.Request.Method == http.MethodDelete || strings.HasSuffix(c.Request.URL.Path, "/deactivate")) && !claims.HardDelete {
				abort(c, http.StatusForbidden, "additional permission required")
				return
			}
			principal, err := d.principalFromClaims(c, claims)
			if err != nil {
				abort(c, http.StatusUnauthorized, "authentication required")
				return
			}
			c.Set(principalKey, principal)
			c.Next()
			return
		}

		if d.DisableLegacyConsole {
			abort(c, http.StatusUnauthorized, "authentication required")
			return
		}
		cookie, err := c.Cookie(sessionCookie)
		if err != nil || cookie == "" {
			abort(c, http.StatusUnauthorized, "authentication required")
			return
		}
		session, err := d.Sessions.Decode(cookie)
		if err != nil {
			abort(c, http.StatusUnauthorized, "authentication required")
			return
		}
		// Re-checked per request, not just at sign-in: taking someone off the
		// allowlist must end their access now, not in eight hours.
		if !d.Allowlist.Allows(session.Email) {
			abort(c, http.StatusUnauthorized, "authentication required")
			return
		}
		if isUnsafe(c.Request.Method) && !auth.SameCSRF(session.CSRF, c.GetHeader("X-CSRF-Token")) {
			abort(c, http.StatusForbidden, "missing or invalid CSRF token")
			return
		}
		principal, err := d.principalFromClaims(c, auth.Claims{
			Subject: session.Subject, Email: session.Email, OrgID: session.OrgID, Roles: session.Roles,
		})
		if err != nil {
			abort(c, http.StatusUnauthorized, "authentication required")
			return
		}
		c.Set(principalKey, principal)
		c.Set(sessionKey, session)
		c.Next()
	}
}

func (d Deps) principalFromClaims(c *gin.Context, claims auth.Claims) (authz.Principal, error) {
	ctx := c.Request.Context()

	// A machine user that maps to a registered product is that product.
	if prod, err := d.Store.ProductByMachineUser(ctx, claims.Subject); err == nil {
		return authz.Principal{Kind: authz.KindProduct, Subject: claims.Subject, ProductKey: prod.Key}, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return authz.Principal{}, err
	}

	if claims.OrgID == d.PlatformOrgID && hasPlatformRole(claims.Roles) {
		return authz.Principal{
			Kind: authz.KindPlatform, Subject: claims.Subject, Email: claims.Email,
			EmailVerified: claims.EmailVerified, ExternalOrgID: claims.OrgID, Roles: claims.Roles,
			ConsoleAccess: claims.Console,
		}, nil
	}
	if claims.OrgID == d.PlatformOrgID && claims.EmailVerified {
		return authz.Principal{
			Kind: authz.KindUser, Subject: claims.Subject, Email: claims.Email,
			EmailVerified: true, ExternalOrgID: claims.OrgID, Roles: claims.Roles,
		}, nil
	}

	// Customer login claims require a live organization-owner membership.
	// Machine users created for Registry publishing have no verified email and
	// cannot use this human credential-management API directly.
	if !claims.EmailVerified {
		return authz.Principal{}, auth.ErrInvalidToken
	}
	org, err := d.Store.OrganizationByZitadelID(ctx, claims.OrgID)
	if err != nil {
		return authz.Principal{}, err
	}
	owner, err := d.Service.IsOrganizationOwner(ctx, org.ZitadelOrgID, claims.Subject)
	if err != nil || !owner {
		return authz.Principal{}, auth.ErrInvalidToken
	}
	return authz.Principal{
		Kind: authz.KindOrgAdmin, Subject: claims.Subject, Email: claims.Email,
		EmailVerified: true, OrgID: org.ID.String(), ExternalOrgID: claims.OrgID, Roles: claims.Roles,
	}, nil
}

func hasPlatformRole(roles []string) bool {
	for _, role := range roles {
		if role == authz.RolePlatformAdmin || role == authz.RolePlatformOperator {
			return true
		}
	}
	return false
}

func bearer(c *gin.Context) string {
	header := c.GetHeader("Authorization")
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

func isUnsafe(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

func principalOf(c *gin.Context) authz.Principal {
	if p, ok := c.Get(principalKey); ok {
		return p.(authz.Principal)
	}
	return authz.Principal{}
}

// idempotency replays the first response for a repeated Idempotency-Key, and
// refuses the key if the body changed — a retry of a different request is a
// bug, not a retry.
func (d Deps) idempotency() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Staff commands use durable claims and current authorization, never replay-cache authority.
		const recovery = "/v1/orgs/:id/staff-identities/:commandId/recovery"
		path := c.FullPath()
		if path == recovery || strings.HasPrefix(path, recovery+"/") {
			c.Next()
			return
		}
		switch path {
		case "/v1/orgs/:id/staff-identities/:commandId/observations",
			"/v1/orgs/:id/staff-identities/:commandId":
			c.Next()
			return
		}
		key := c.GetHeader("Idempotency-Key")
		if key == "" || !isUnsafe(c.Request.Method) {
			c.Next()
			return
		}
		if len(key) > 200 {
			abort(c, http.StatusBadRequest, "Idempotency-Key is too long")
			return
		}

		actor := principalOf(c).Subject
		body, _ := c.Get(bodyKey)
		raw, _ := body.([]byte)
		sum := sha256.Sum256(append([]byte(c.Request.Method+c.Request.URL.Path+"\n"), raw...))
		hash := hex.EncodeToString(sum[:])

		stored, err := d.Store.Idempotent(c.Request.Context(), actor, key, hash)
		switch {
		case err == nil:
			c.Header("Idempotent-Replay", "true")
			c.Data(stored.Status, "application/json", stored.Body)
			c.Abort()
			return
		case errors.Is(err, store.ErrIdempotencyConflict):
			abort(c, http.StatusConflict, "this Idempotency-Key was used for a different request")
			return
		case !errors.Is(err, store.ErrNotFound):
			respondErr(c, d.logger(), err)
			return
		}

		recorder := &capturingWriter{ResponseWriter: c.Writer}
		c.Writer = recorder
		c.Next()

		if recorder.Status() >= 200 && recorder.Status() < 300 {
			status, responseBody := recorder.Status(), recorder.body
			if isOneTimeSecretRoute(c) {
				// Persisting the successful response would persist the OAuth client
				// secret. A replay therefore becomes a safe conflict: the caller can
				// list the created credential and rotate it for a new one-time value.
				status = http.StatusConflict
				responseBody = []byte(`{"type":"https://onboard.tesserix.app/problems/Conflict","title":"Conflict","status":409,"detail":"the credential secret was already issued; list the credential and rotate it to obtain a new secret"}`)
			}
			if _, err := d.Store.SaveIdempotent(c.Request.Context(), actor, key, hash,
				status, responseBody); err != nil {
				d.logger().Error("could not record idempotency key", "error", err)
			}
		}
	}
}

func isOneTimeSecretRoute(c *gin.Context) bool {
	if c.Request.Method != http.MethodPost {
		return false
	}
	path := c.FullPath()
	return path == "/v1/registry/credentials" || path == "/v1/registry/credentials/:credentialId/rotate"
}

type capturingWriter struct {
	gin.ResponseWriter
	body []byte
}

func (w *capturingWriter) Write(b []byte) (int, error) {
	w.body = append(w.body, b...)
	return w.ResponseWriter.Write(b)
}

// rateLimiter is per-principal and in-process. Two replicas therefore allow
// twice the rate; it is a brake on a runaway client, not a quota.
type rateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
	perSec   rate.Limit
	burst    int
}

func newRateLimiter(perSec float64, burst int) *rateLimiter {
	return &rateLimiter{limiters: map[string]*rate.Limiter{}, perSec: rate.Limit(perSec), burst: burst}
}

func (r *rateLimiter) allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	limiter, ok := r.limiters[key]
	if !ok {
		limiter = rate.NewLimiter(r.perSec, r.burst)
		r.limiters[key] = limiter
	}
	return limiter.Allow()
}

func (r *rateLimiter) middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := principalOf(c).Subject
		if key == "" {
			key = c.ClientIP()
		}
		if !r.allow(key) {
			abort(c, http.StatusTooManyRequests, "too many requests")
			return
		}
		c.Next()
	}
}
