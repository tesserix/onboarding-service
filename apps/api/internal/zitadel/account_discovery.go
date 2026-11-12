package zitadel

import (
	"context"
	"github.com/google/uuid"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DiscoveryAccount is an active native human; product and business admission remain separate.
type DiscoveryAccount struct{ TenantID, Subject string }

// DiscoveryAccounts is private and used only after a browser proves mailbox control.
func (c *Client) DiscoveryAccounts(ctx context.Context, email string) ([]DiscoveryAccount, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return nil, ErrIdentityUnavailable
	}
	var wire struct {
		Details *struct {
			Total     *string `json:"totalResult"`
			Timestamp string  `json:"timestamp"`
		} `json:"details"`
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
	input := map[string]any{"query": map[string]string{"limit": "21"}, "queries": []map[string]any{{"emailQuery": map[string]string{"emailAddress": email, "method": "TEXT_QUERY_METHOD_EQUALS_IGNORE_CASE"}}}}
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if err = c.readIdentity(ctx, &client, token, http.MethodPost, "/v2/users", "", input, &wire); err != nil {
		return nil, err
	}
	if wire.Details == nil {
		return nil, ErrIdentityUnavailable
	}
	total := uint64(0)
	if wire.Details.Total != nil {
		total, err = strconv.ParseUint(*wire.Details.Total, 10, 64)
		if err != nil {
			return nil, ErrIdentityUnavailable
		}
	} else if _, err = time.Parse(time.RFC3339Nano, wire.Details.Timestamp); err != nil || len(wire.Result) != 0 {
		return nil, ErrIdentityUnavailable
	}
	if total > 20 || total != uint64(len(wire.Result)) {
		return nil, ErrIdentityUnavailable
	}
	result := []DiscoveryAccount{}
	seen := map[string]bool{}
	for _, user := range wire.Result {
		if user.Human == nil {
			continue
		}
		if !strings.EqualFold(user.Human.Email.Address, email) || !identitySourceID.MatchString(user.ID) {
			return nil, ErrIdentityUnavailable
		}
		tenantID, owned := strings.CutPrefix(user.Details.ResourceOwner, "helivanta-")
		if !owned || user.State != "USER_STATE_ACTIVE" || !user.Human.Email.Verified {
			continue
		}
		tenant, e := uuid.Parse(tenantID)
		if e != nil || tenant == uuid.Nil || tenant.String() != tenantID || seen[tenantID] {
			return nil, ErrIdentityUnavailable
		}
		seen[tenantID] = true
		result = append(result, DiscoveryAccount{tenantID, user.ID})
	}
	return result, nil
}
