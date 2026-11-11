package zitadel

import (
	"context"
	"net/http"
	"time"
)

// SignupCode returns native proof only to the private product email delivery path.
func (c *Client) SignupCode(ctx context.Context, userID string, password bool) (string, error) {
	if !identitySourceID.MatchString(userID) {
		return "", ErrSignupProof
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return "", ErrIdentityUnavailable
	}
	path := "/v2/users/" + userID + "/email/send"
	if password {
		path = "/v2/users/" + userID + "/password_reset"
	}
	var result struct {
		Code string `json:"verificationCode"`
	}
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.readIdentity(ctx, &client, token, http.MethodPost, path, "", map[string]any{"returnCode": map[string]any{}}, &result) != nil || !validSignupCode(userID, result.Code) {
		return "", ErrIdentityUnavailable
	}
	return result.Code, nil
}
