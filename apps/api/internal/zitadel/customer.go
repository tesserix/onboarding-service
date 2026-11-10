package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrSignupProof = errors.New("signup proof rejected")

// VerifySignupEmail submits the caller's native code for the expected signup subject.
func (c *Client) VerifySignupEmail(ctx context.Context, userID, code string) error {
	if !validSignupCode(userID, code) {
		return ErrSignupProof
	}
	return c.signupCommand(ctx, "/v2/users/"+userID+"/email/verify", map[string]string{"verificationCode": code})
}

func validSignupCode(userID, code string) bool {
	return identitySourceID.MatchString(userID) && code != "" && len(code) <= 20 && !strings.ContainsFunc(code, unicode.IsControl)
}

func (c *Client) signupCommand(ctx context.Context, path string, input any) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return ErrIdentityUnavailable
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return ErrIdentityUnavailable
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(raw))
	if err != nil {
		return ErrIdentityUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		return ErrIdentityUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 8192))
	if response.StatusCode == 400 || response.StatusCode == 412 {
		return ErrSignupProof
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrIdentityUnavailable
	}
	return nil
}

// SetSignupPassword always requires the native reset proof, including first setup.
func (c *Client) SetSignupPassword(ctx context.Context, userID, code, password string) error {
	if !validSignupCode(userID, code) || password == "" || !utf8.ValidString(password) || utf8.RuneCountInString(password) > 200 {
		return ErrSignupProof
	}
	return c.signupCommand(ctx, "/v2/users/"+userID+"/password", map[string]any{"verificationCode": code, "newPassword": map[string]any{"password": password, "changeRequired": false}})
}

// SendSignupPasswordLink asks native identity to deliver a setup code, never return it.
func (c *Client) SendSignupPasswordLink(ctx context.Context, userID string) error {
	if !identitySourceID.MatchString(userID) {
		return ErrSignupProof
	}
	return c.signupCommand(ctx, "/v2/users/"+userID+"/password_reset", map[string]any{"sendLink": map[string]string{"urlTemplate": "https://helivanta.app/signup/verify#phase=password&userId={{.UserID}}&code={{.Code}}", "notificationType": "NOTIFICATION_TYPE_Email"}})
}
