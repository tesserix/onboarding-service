package api

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
)

// problem is an RFC 7807 document. The detail is always a sentence we wrote —
// never a wrapped driver error, which would leak schema and library versions.
type problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Detail    string `json:"detail,omitempty"`
	RequestID string `json:"requestId,omitempty"`
}

func abort(c *gin.Context, status int, detail string) {
	c.Header("Content-Type", "application/problem+json")
	c.AbortWithStatusJSON(status, problem{
		Type:      "https://onboard.tesserix.app/problems/" + http.StatusText(status),
		Title:     http.StatusText(status),
		Status:    status,
		Detail:    detail,
		RequestID: c.GetString(requestIDKey),
	})
}

// respondErr is the single mapping from domain errors to status codes. An
// unrecognised error is a 500 with the detail logged, not returned.
func respondErr(c *gin.Context, log *slog.Logger, err error) {
	switch {
	case errors.Is(err, onboarding.ErrNotFound):
		abort(c, http.StatusNotFound, "not found")
	case errors.Is(err, onboarding.ErrInvalid):
		abort(c, http.StatusBadRequest, trimSentinel(err, onboarding.ErrInvalid))
	case errors.Is(err, onboarding.ErrUnavailable):
		abort(c, http.StatusServiceUnavailable, "a dependency is unavailable; no change was made")
	case errors.Is(err, authz.ErrDenied):
		abort(c, http.StatusForbidden, "not permitted")
	case errors.Is(err, auth.ErrInvalidToken), errors.Is(err, auth.ErrInvalidSession):
		abort(c, http.StatusUnauthorized, "authentication required")
	default:
		log.Error("unhandled error", "path", c.FullPath(), "requestId", c.GetString(requestIDKey), "error", err)
		abort(c, http.StatusInternalServerError, "internal error")
	}
}

// trimSentinel returns the caller-safe half of "invalid request: <detail>".
func trimSentinel(err, sentinel error) string {
	msg := err.Error()
	prefix := sentinel.Error() + ": "
	if len(msg) > len(prefix) && msg[:len(prefix)] == prefix {
		return msg[len(prefix):]
	}
	return msg
}
