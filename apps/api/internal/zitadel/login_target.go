package zitadel

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// TenantLoginSubject searches only the organisation selected by the product owner.
func (c *Client) TenantLoginSubject(ctx context.Context, org, email string) (string, error) {
	if !identitySourceID.MatchString(org) || email == "" || len(email) > 200 {
		return "", ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return "", ErrIdentityUnavailable
	}
	var wire struct {
		Result []struct {
			ID      string          `json:"userId"`
			State   string          `json:"state"`
			Details identityDetails `json:"details"`
			Human   *struct {
				Email struct {
					Address  string `json:"email"`
					Verified bool   `json:"isVerified"`
				} `json:"email"`
			} `json:"human"`
		} `json:"result"`
	}
	input := map[string]any{"query": map[string]string{"limit": "2"}, "queries": []map[string]any{
		{"organizationIdQuery": map[string]string{"organizationId": org}},
		{"emailQuery": map[string]string{"emailAddress": email, "method": "TEXT_QUERY_METHOD_EQUALS_IGNORE_CASE"}},
	}}
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if err = c.readIdentity(ctx, &client, token, http.MethodPost, "/v2/users", org, input, &wire); err != nil {
		return "", err
	}
	if len(wire.Result) != 1 {
		return "", ErrIdentityMissing
	}
	u := wire.Result[0]
	if !identitySourceID.MatchString(u.ID) || u.State != "USER_STATE_ACTIVE" || u.Details.ResourceOwner != org || u.Human == nil || !u.Human.Email.Verified || !strings.EqualFold(u.Human.Email.Address, email) {
		return "", ErrIdentityMissing
	}
	return u.ID, nil
}
