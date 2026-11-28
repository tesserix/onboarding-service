package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// StaffPasswordConfigured observes credential presence without reading or changing it.
func (c *Client) StaffPasswordConfigured(ctx context.Context, org, user string) (bool, error) {
	if !identitySourceID.MatchString(org) || !identitySourceID.MatchString(user) {
		return false, ErrIdentityUnavailable
	}
	return c.passwordConfigured(ctx, org, user)
}

func (c *Client) passwordConfigured(ctx context.Context, org, user string) (bool, error) {
	methods, err := c.authenticationMethods(ctx, org, user)
	return methods["AUTHENTICATION_METHOD_TYPE_PASSWORD"], err
}

// StaffSecondFactorsConfigured reports whether a TOTP or a security key is
// configured. It is a pre-dispatch presence check, never enrollment proof.
func (c *Client) StaffSecondFactorsConfigured(ctx context.Context, org, user string) (bool, error) {
	if c == nil || c.tokens == nil || c.http == nil || !identitySourceID.MatchString(org) || !identitySourceID.MatchString(user) {
		return false, ErrIdentityUnavailable
	}
	methods, err := c.authenticationMethods(ctx, org, user)
	return methods["AUTHENTICATION_METHOD_TYPE_TOTP"] || methods["AUTHENTICATION_METHOD_TYPE_U2F"], err
}

func (c *Client) authenticationMethods(ctx context.Context, org, user string) (map[string]bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return nil, ErrIdentityUnavailable
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
	var raw json.RawMessage
	if c.readIdentity(ctx, &client, token, http.MethodGet, "/v2/users/"+user+"/authentication_methods", org, nil, &raw) != nil {
		return nil, ErrIdentityUnavailable
	}
	fields, err := identityObject(raw, "details", "authMethodTypes")
	if err != nil {
		return nil, ErrIdentityUnavailable
	}
	details, err := identityObject(fields["details"], "totalResult", "timestamp")
	if err != nil {
		return nil, ErrIdentityUnavailable
	}
	for _, value := range []json.RawMessage{fields["authMethodTypes"], details["totalResult"]} {
		if len(value) != 0 && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, ErrIdentityUnavailable
		}
	}
	if json.Unmarshal(raw, &result) != nil || result.Details == nil || len(result.Methods) > 8 {
		return nil, ErrIdentityUnavailable
	}
	if result.Details.Total == nil {
		if _, err := time.Parse(time.RFC3339Nano, result.Details.Timestamp); err != nil || len(result.Methods) != 0 {
			return nil, ErrIdentityUnavailable
		}
	} else if n, err := strconv.ParseUint(*result.Details.Total, 10, 32); err != nil || n != uint64(len(result.Methods)) {
		return nil, ErrIdentityUnavailable
	}
	seen := make(map[string]bool, len(result.Methods))
	for _, method := range result.Methods {
		if seen[method] {
			return nil, ErrIdentityUnavailable
		}
		seen[method] = true
		switch method {
		case "AUTHENTICATION_METHOD_TYPE_PASSWORD", "AUTHENTICATION_METHOD_TYPE_PASSKEY", "AUTHENTICATION_METHOD_TYPE_IDP", "AUTHENTICATION_METHOD_TYPE_TOTP", "AUTHENTICATION_METHOD_TYPE_U2F", "AUTHENTICATION_METHOD_TYPE_OTP_SMS", "AUTHENTICATION_METHOD_TYPE_OTP_EMAIL", "AUTHENTICATION_METHOD_TYPE_RECOVERY_CODE":
		default:
			return nil, ErrIdentityUnavailable
		}
	}
	return seen, nil
}
