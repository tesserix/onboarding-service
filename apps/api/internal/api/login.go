package api

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tesserix/onboarding-service/api/internal/auth"
	"github.com/tesserix/onboarding-service/api/internal/authz"
)

const (
	loginStateCookie = "onboard_login"
	sessionTTL       = 8 * time.Hour
)

func (d Deps) registerAuthRoutes(r *gin.Engine) {
	r.GET("/api/auth/login", d.login)
	r.GET("/api/auth/callback", d.callback)
	r.POST("/api/auth/logout", d.logout)
}

func (d Deps) login(c *gin.Context) {
	verifier, challenge, err := auth.NewPKCE()
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	state, err := auth.NewCSRFToken()
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	nonce, err := auth.NewCSRFToken()
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}

	// The PKCE verifier and state ride in a sealed cookie rather than server
	// memory, so a login survives a rolling restart or a second replica.
	sealed, err := d.Sessions.Encode(auth.Session{
		Subject: state, CSRF: nonce, Email: verifier, ExpiresAt: time.Now().Add(10 * time.Minute),
	})
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	d.setCookie(c, loginStateCookie, sealed, 600)
	c.Redirect(http.StatusFound, d.OIDC.AuthorizeURL(state, nonce, challenge))
}

func (d Deps) callback(c *gin.Context) {
	sealed, err := c.Cookie(loginStateCookie)
	if err != nil || sealed == "" {
		abort(c, http.StatusBadRequest, "the login has expired; start again")
		return
	}
	pending, err := d.Sessions.Decode(sealed)
	if err != nil {
		abort(c, http.StatusBadRequest, "the login has expired; start again")
		return
	}
	// The state binds the callback to the browser that started the login.
	if !auth.SameCSRF(pending.Subject, c.Query("state")) {
		abort(c, http.StatusBadRequest, "the login could not be verified; start again")
		return
	}
	d.setCookie(c, loginStateCookie, "", -1)

	tokens, err := d.OIDC.Exchange(c.Request.Context(), c.Query("code"), pending.Email)
	if err != nil {
		d.logger().Warn("token exchange failed", "requestId", c.GetString(requestIDKey), "error", err)
		abort(c, http.StatusUnauthorized, "sign-in failed")
		return
	}
	claims, err := d.ConsoleTokens.Verify(c.Request.Context(), tokens.IDToken)
	if err != nil {
		abort(c, http.StatusUnauthorized, "sign-in failed")
		return
	}
	// The nonce binds the ID token to this login, so one obtained elsewhere for
	// the same client cannot be replayed here.
	if !auth.SameCSRF(pending.CSRF, claims.Nonce) {
		abort(c, http.StatusUnauthorized, "sign-in failed")
		return
	}
	if !claims.EmailVerified || !d.Allowlist.Allows(claims.Email) {
		d.logger().Warn("console sign-in refused",
			"requestId", c.GetString(requestIDKey), "subject", claims.Subject,
			"emailVerified", claims.EmailVerified)
		abort(c, http.StatusForbidden, "this account is not permitted to use the console")
		return
	}

	csrf, err := auth.NewCSRFToken()
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	// Being on the allowlist is what makes someone platform staff; Google says
	// who they are and nothing about what they may do.
	session, err := d.Sessions.Encode(auth.Session{
		Subject: claims.Subject, Email: claims.Email, OrgID: d.PlatformOrgID,
		Roles: []string{authz.RolePlatformAdmin},
		CSRF:  csrf, ExpiresAt: time.Now().Add(sessionTTL),
	})
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	d.setCookie(c, sessionCookie, session, int(sessionTTL.Seconds()))
	// The console reads the CSRF token from this cookie, so it is deliberately
	// not HttpOnly; it is a value to echo back, not a credential on its own.
	c.SetCookie("onboard_csrf", csrf, int(sessionTTL.Seconds()), "/", "", d.SecureCookies, false)
	c.Redirect(http.StatusFound, orDefault(d.ConsoleURL, "/"))
}

func (d Deps) logout(c *gin.Context) {
	d.setCookie(c, sessionCookie, "", -1)
	c.SetCookie("onboard_csrf", "", -1, "/", "", d.SecureCookies, false)
	c.JSON(http.StatusOK, gin.H{"status": "signed out"})
}

func (d Deps) setCookie(c *gin.Context, name, value string, maxAge int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: d.SecureCookies, SameSite: http.SameSiteLaxMode,
	})
}
