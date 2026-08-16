package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
)

func respondTo(t *testing.T, err error) (*httptest.ResponseRecorder, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	var logged bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/orgs", nil)
	respondErr(c, log, err)
	return rec, logged.String()
}

// A 503 with no log line is undiagnosable: the client is told only that "a
// dependency is unavailable", and the cause exists nowhere. This cost a whole
// debugging session against Zitadel's own logs.
func TestAServerSideFailureLogsItsCause(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"unavailable", fmt.Errorf("%w: set tenant_id: 404 not found", onboarding.ErrUnavailable), http.StatusServiceUnavailable},
		{"unhandled", errors.New("dial tcp: connection refused"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, logged := respondTo(t, tc.err)
			if rec.Code != tc.want {
				t.Fatalf("status %d, want %d", rec.Code, tc.want)
			}
			if !strings.Contains(logged, tc.err.Error()) {
				t.Fatalf("cause never logged; log was %q", logged)
			}
		})
	}
}

// A client mistake is not an incident. Logging every 400 and 403 at error level
// buries the failures that matter.
func TestClientErrorsAreNotLoggedAsErrors(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("%w: name is required", onboarding.ErrInvalid),
		onboarding.ErrNotFound,
		authz.ErrDenied,
	} {
		_, logged := respondTo(t, err)
		if strings.Contains(logged, `"level":"ERROR"`) {
			t.Fatalf("%v logged at ERROR: %s", err, logged)
		}
	}
}

// The response body carries our own sentence and a request id — never the
// wrapped cause, which leaks upstream hostnames and library detail.
func TestTheCauseNeverReachesTheClient(t *testing.T) {
	rec, _ := respondTo(t, fmt.Errorf("%w: set tenant_id: dial zitadel.internal:8080", onboarding.ErrUnavailable))

	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.Detail, "zitadel.internal") {
		t.Fatalf("upstream detail leaked to the client: %q", p.Detail)
	}
}
