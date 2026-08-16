package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tesserix/onboarding-service/api/internal/auth"
)

// The console's identity provider is Google; these tests drive the callback
// with a stub verifier so the gate itself is what is under test, not Google.
type consoleHarness struct {
	router   *gin.Engine
	sessions *auth.SessionCodec
	tokens   *stubTokens
}

func newConsoleHarness(t *testing.T, allow string) *consoleHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)

	sessions, err := auth.NewSessionCodec(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	tokens := &stubTokens{claims: map[string]auth.Claims{}}
	google := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id_token":"google-id-token"}`))
	}))
	t.Cleanup(google.Close)
	oidc := auth.NewGoogleClient("client-1", "secret-1", "https://onboard.tesserix.app/api/auth/callback")
	oidc.TokenEndpoint, oidc.HTTP = google.URL, google.Client()

	d := Deps{
		Sessions:      sessions,
		ConsoleTokens: tokens,
		Tokens:        tokens,
		OIDC:          oidc,
		Allowlist:     auth.ParseAllowlist(allow),
		PlatformOrgID: platformOrg,
		ConsoleURL:    "https://onboard.tesserix.app/",
	}
	return &consoleHarness{router: NewRouter(d), sessions: sessions, tokens: tokens}
}

// startLogin runs /api/auth/login and returns the state, nonce and login cookie
// the callback will be checked against.
func (h *consoleHarness) startLogin(t *testing.T) (state, nonce string, cookie *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/login", nil))
	if rec.Code != http.StatusFound {
		t.Fatalf("login did not redirect: %d", rec.Code)
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == loginStateCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("login did not set its state cookie")
	}
	return u.Query().Get("state"), u.Query().Get("nonce"), cookie
}

func (h *consoleHarness) callback(t *testing.T, claims auth.Claims, state string, cookie *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	h.tokens.claims["google-id-token"] = claims
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=code-1&state="+url.QueryEscape(state), nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

func sessionCookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			return c
		}
	}
	return nil
}

func TestConsoleSignInAdmitsAnAllowlistedAddress(t *testing.T) {
	h := newConsoleHarness(t, "first@example.test,second@example.test")
	state, nonce, cookie := h.startLogin(t)

	rec := h.callback(t, auth.Claims{
		Subject: "google-1", Email: "First@Example.test", EmailVerified: true, Nonce: nonce,
	}, state, cookie)

	if rec.Code != http.StatusFound {
		t.Fatalf("sign-in rejected: %d %s", rec.Code, rec.Body.String())
	}
	set := sessionCookieOf(rec)
	if set == nil {
		t.Fatal("no session cookie was issued")
	}
	session, err := h.sessions.Decode(set.Value)
	if err != nil {
		t.Fatal(err)
	}
	// The console is platform staff: the session must carry the platform org, or
	// every request lands as an org admin of nothing.
	if session.OrgID != platformOrg {
		t.Errorf("session org %q, want %q", session.OrgID, platformOrg)
	}
	if len(session.Roles) == 0 {
		t.Error("session carries no platform role")
	}
}

func TestConsoleSignInRefusesAnyoneNotOnTheAllowlist(t *testing.T) {
	h := newConsoleHarness(t, "first@example.test")
	state, nonce, cookie := h.startLogin(t)

	rec := h.callback(t, auth.Claims{
		Subject: "google-2", Email: "attacker@example.test", EmailVerified: true, Nonce: nonce,
	}, state, cookie)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
	if sessionCookieOf(rec) != nil {
		t.Fatal("a session was issued to an address that is not on the allowlist")
	}
}

// Google will not assert an address it has not verified; accepting one would let
// an unverified mailbox impersonate a listed address.
func TestConsoleSignInRefusesAnUnverifiedAddress(t *testing.T) {
	h := newConsoleHarness(t, "first@example.test")
	state, nonce, cookie := h.startLogin(t)

	rec := h.callback(t, auth.Claims{
		Subject: "google-3", Email: "first@example.test", EmailVerified: false, Nonce: nonce,
	}, state, cookie)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rec.Code)
	}
	if sessionCookieOf(rec) != nil {
		t.Fatal("a session was issued for an unverified address")
	}
}

// The nonce binds the ID token to this login; without the check an ID token
// obtained elsewhere for the same client can be replayed into this callback.
func TestConsoleSignInRefusesAnIDTokenMintedForAnotherLogin(t *testing.T) {
	h := newConsoleHarness(t, "first@example.test")
	state, _, cookie := h.startLogin(t)

	rec := h.callback(t, auth.Claims{
		Subject: "google-4", Email: "first@example.test", EmailVerified: true, Nonce: "someone-elses-nonce",
	}, state, cookie)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	if sessionCookieOf(rec) != nil {
		t.Fatal("a replayed id token produced a session")
	}
}

// Removing an address from the chart must end that person's access without
// waiting out their session.
func TestASessionForARemovedAddressStopsWorking(t *testing.T) {
	h := newConsoleHarness(t, "first@example.test")
	sealed, err := h.sessions.Encode(auth.Session{
		Subject: "google-5", Email: "removed@example.test", OrgID: platformOrg,
		CSRF: "csrf", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/orgs", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sealed})
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

func TestLoginSendsTheBrowserToGoogle(t *testing.T) {
	h := newConsoleHarness(t, "first@example.test")
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/login", nil))

	if got := rec.Header().Get("Location"); !strings.HasPrefix(got, auth.GoogleAuthorizeEndpoint) {
		t.Fatalf("login goes to %q", got)
	}
}
