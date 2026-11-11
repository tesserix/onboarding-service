package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
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

// SignupIdentity reads the current native binding rather than trusting an email or supplied subject.
func (c *Client) SignupIdentity(ctx context.Context, orgID, userID, email string) (bool, error) {
	if !identitySourceID.MatchString(orgID) || !identitySourceID.MatchString(userID) || email == "" {
		return false, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return false, ErrIdentityUnavailable
	}
	var result struct {
		User struct {
			ID      string `json:"userId"`
			State   string `json:"state"`
			Details struct {
				ResourceOwner string `json:"resourceOwner"`
			} `json:"details"`
			Human *struct {
				Email struct {
					Address  string `json:"email"`
					Verified bool   `json:"isVerified"`
				} `json:"email"`
			} `json:"human"`
		} `json:"user"`
	}
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if err = c.readIdentity(ctx, &client, token, http.MethodGet, "/v2/users/"+userID, orgID, nil, &result); err != nil {
		return false, err
	}
	user := result.User
	if user.ID != userID || user.State != "USER_STATE_ACTIVE" || user.Details.ResourceOwner != orgID || user.Human == nil || !strings.EqualFold(user.Human.Email.Address, email) {
		return false, ErrIdentityUnavailable
	}
	return user.Human.Email.Verified, nil
}

// OtherSignupIdentity detects an existing person without adopting them by email.
func (c *Client) OtherSignupIdentity(ctx context.Context, email, expectedUserID string) (bool, error) {
	if !identitySourceID.MatchString(expectedUserID) || email == "" || utf8.RuneCountInString(email) > 200 {
		return false, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return false, ErrIdentityUnavailable
	}
	var result struct {
		Details *struct {
			Total     *string `json:"totalResult"`
			Timestamp string  `json:"timestamp"`
		} `json:"details"`
		Result []struct {
			ID    string `json:"userId"`
			Human *struct {
				Email struct {
					Address string `json:"email"`
				} `json:"email"`
			} `json:"human"`
		} `json:"result"`
	}
	input := map[string]any{"query": map[string]string{"limit": "2"}, "queries": []map[string]any{{"emailQuery": map[string]string{"emailAddress": email, "method": "TEXT_QUERY_METHOD_EQUALS_IGNORE_CASE"}}}}
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if err = c.readIdentity(ctx, &client, token, http.MethodPost, "/v2/users", "", input, &result); err != nil {
		return false, err
	}
	if result.Details == nil {
		return false, ErrIdentityUnavailable
	}
	count := "0"
	if result.Details.Total != nil {
		count = *result.Details.Total
	} else {
		// ProtoJSON omits scalar zero and empty repeated fields in native search results.
		if _, err := time.Parse(time.RFC3339Nano, result.Details.Timestamp); err != nil || len(result.Result) != 0 {
			return false, ErrIdentityUnavailable
		}
	}
	total, err := strconv.ParseUint(count, 10, 64)
	if err != nil || total < uint64(len(result.Result)) || len(result.Result) > 2 || uint64(len(result.Result)) != min(total, 2) {
		return false, ErrIdentityUnavailable
	}
	other := total > 1
	for _, user := range result.Result {
		if user.ID == "" || user.Human == nil || !strings.EqualFold(user.Human.Email.Address, email) {
			return false, ErrIdentityUnavailable
		}
		if user.ID != expectedUserID {
			other = true
		}
	}
	return other, nil
}

// SendSignupEmailLink resends native verification for the expected signup subject.
func (c *Client) SendSignupEmailLink(ctx context.Context, userID string) error {
	if !identitySourceID.MatchString(userID) {
		return ErrSignupProof
	}
	return c.signupCommand(ctx, "/v2/users/"+userID+"/email/send", map[string]any{"sendCode": map[string]string{"urlTemplate": "https://helivanta.app/signup/verify#phase=email&userId={{.UserID}}&code={{.Code}}"}})
}
