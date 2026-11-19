package zitadel

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// TenantLoginSubject searches only the organisation selected by the product owner.
func (c *Client) TenantLoginSubject(ctx context.Context, org, email string) (string, error) {
	account, err := c.tenantMailboxAccount(ctx, org, email, false)
	return account.Subject, err
}

// RecoveryAccount is native state for a mailbox already verified by the recovery owner.
type RecoveryAccount struct {
	Subject string
	Locked  bool
}

// TenantRecoveryAccount admits a locked-account observation, never ordinary login authority.
func (c *Client) TenantRecoveryAccount(ctx context.Context, org, email string) (RecoveryAccount, error) {
	return c.tenantMailboxAccount(ctx, org, email, true)
}

func (c *Client) tenantMailboxAccount(ctx context.Context, org, email string, recovery bool) (RecoveryAccount, error) {
	if !identitySourceID.MatchString(org) || email == "" || len(email) > 200 {
		return RecoveryAccount{}, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return RecoveryAccount{}, ErrIdentityUnavailable
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
		return RecoveryAccount{}, err
	}
	if len(wire.Result) != 1 {
		return RecoveryAccount{}, ErrIdentityMissing
	}
	u := wire.Result[0]
	eligibleState := u.State == "USER_STATE_ACTIVE" || recovery && u.State == "USER_STATE_LOCKED"
	if !identitySourceID.MatchString(u.ID) || !eligibleState || u.Details.ResourceOwner != org || u.Human == nil || !u.Human.Email.Verified || !strings.EqualFold(u.Human.Email.Address, email) {
		return RecoveryAccount{}, ErrIdentityMissing
	}
	return RecoveryAccount{Subject: u.ID, Locked: u.State == "USER_STATE_LOCKED"}, nil
}
