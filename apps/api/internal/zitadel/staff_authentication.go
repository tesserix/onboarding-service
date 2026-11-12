package zitadel

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// StaffPasswordConfigured observes credential presence without reading or changing it.
func (c *Client) StaffPasswordConfigured(ctx context.Context, org, user string) (bool, error) {
	if !identitySourceID.MatchString(org) || !identitySourceID.MatchString(user) {
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
		Methods []string `json:"authMethodTypes"`
	}
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if c.readIdentity(ctx, &client, token, http.MethodGet, "/v2/users/"+user+"/authentication_methods", org, nil, &result) != nil || result.Details == nil || len(result.Methods) > 8 {
		return false, ErrIdentityUnavailable
	}
	if result.Details.Total == nil {
		if _, err := time.Parse(time.RFC3339Nano, result.Details.Timestamp); err != nil || len(result.Methods) != 0 {
			return false, ErrIdentityUnavailable
		}
	} else if n, err := strconv.ParseUint(*result.Details.Total, 10, 32); err != nil || n != uint64(len(result.Methods)) {
		return false, ErrIdentityUnavailable
	}
	seen := make(map[string]bool, len(result.Methods))
	password := false
	for _, method := range result.Methods {
		if seen[method] {
			return false, ErrIdentityUnavailable
		}
		seen[method] = true
		switch method {
		case "AUTHENTICATION_METHOD_TYPE_PASSWORD":
			password = true
		case "AUTHENTICATION_METHOD_TYPE_PASSKEY", "AUTHENTICATION_METHOD_TYPE_IDP", "AUTHENTICATION_METHOD_TYPE_TOTP", "AUTHENTICATION_METHOD_TYPE_U2F", "AUTHENTICATION_METHOD_TYPE_OTP_SMS", "AUTHENTICATION_METHOD_TYPE_OTP_EMAIL", "AUTHENTICATION_METHOD_TYPE_RECOVERY_CODE":
		default:
			return false, ErrIdentityUnavailable
		}
	}
	return password, nil
}
