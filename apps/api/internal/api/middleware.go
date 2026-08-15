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
			if err != nil {
				abort(c, http.StatusUnauthorized, "authentication required")
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

	if claims.OrgID == d.PlatformOrgID {
		return authz.Principal{
			Kind: authz.KindPlatform, Subject: claims.Subject, Roles: claims.Roles,
		}, nil
	}

	// Anyone else is an admin of exactly the org their token was issued in.
	org, err := d.Store.OrganizationByZitadelID(ctx, claims.OrgID)
	if err != nil {
		return authz.Principal{}, err
	}
	return authz.Principal{
		Kind: authz.KindOrgAdmin, Subject: claims.Subject, OrgID: org.ID.String(), Roles: claims.Roles,
	}, nil
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
			if _, err := d.Store.SaveIdempotent(c.Request.Context(), actor, key, hash,
				recorder.Status(), recorder.body); err != nil {
				d.logger().Error("could not record idempotency key", "error", err)
			}
		}
	}
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
