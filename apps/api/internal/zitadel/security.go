package zitadel

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"time"
)

type OwnerSecurityProof struct {
	RequireMFA  bool
	ExternalMFA bool
	NoGrace     bool
	Sequence    string
}

// OwnerSecurity rechecks the active human and exact native owner membership on every call.
func (c *Client) OwnerSecurity(ctx context.Context, orgID, subject string) (OwnerSecurityProof, error) {
	var empty OwnerSecurityProof
	if !identitySourceID.MatchString(orgID) || !identitySourceID.MatchString(subject) {
		return empty, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return empty, ErrIdentityUnavailable
	}
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var user struct {
		User struct {
			ID      string          `json:"userId"`
			State   string          `json:"state"`
			Details identityDetails `json:"details"`
			Human   *struct {
				Email struct {
					Verified bool `json:"isVerified"`
				} `json:"email"`
			} `json:"human"`
		} `json:"user"`
	}
	if err = c.readIdentity(ctx, &client, token, http.MethodGet, "/v2/users/"+subject, orgID, nil, &user); err != nil {
		return empty, err
	}
	if user.User.ID != subject || user.User.State != "USER_STATE_ACTIVE" || user.User.Details.ResourceOwner != orgID || user.User.Human == nil || !user.User.Human.Email.Verified {
		return empty, ErrIdentityMissing
	}
	var members struct {
		Details struct {
			Total string `json:"totalResult"`
		} `json:"details"`
		Result []struct {
			UserID  string          `json:"userId"`
			Roles   []string        `json:"roles"`
			Details identityDetails `json:"details"`
		} `json:"result"`
	}
	query := map[string]any{"query": map[string]string{"limit": "2"}, "queries": []map[string]any{{"userIdQuery": map[string]string{"userId": subject}}}}
	if err = c.readIdentity(ctx, &client, token, http.MethodPost, "/management/v1/orgs/me/members/_search", orgID, query, &members); err != nil {
		return empty, err
	}
	if members.Details.Total != "1" || len(members.Result) != 1 || members.Result[0].UserID != subject || members.Result[0].Details.ResourceOwner != orgID || !slices.Contains(members.Result[0].Roles, "ORG_OWNER") {
		return empty, ErrIdentityMissing
	}
	var policy struct {
		IsDefault bool `json:"isDefault"`
		Policy    *struct {
			ForceMFA  bool            `json:"forceMfa"`
			LocalOnly bool            `json:"forceMfaLocalOnly"`
			Grace     string          `json:"mfaInitSkipLifetime"`
			IsDefault bool            `json:"isDefault"`
			Details   identityDetails `json:"details"`
		} `json:"policy"`
	}
	if err = c.readIdentity(ctx, &client, token, http.MethodGet, "/management/v1/policies/login", orgID, nil, &policy); err != nil {
		return empty, err
	}
	if policy.Policy == nil || policy.IsDefault || policy.Policy.IsDefault || policy.Policy.Details.ResourceOwner != orgID {
		return empty, ErrIdentityUnavailable
	}
	p := policy.Policy
	sequence, err := strconv.ParseUint(p.Details.Sequence, 10, 64)
	if err != nil || sequence == 0 || strconv.FormatUint(sequence, 10) != p.Details.Sequence {
		return empty, ErrIdentityUnavailable
	}
	return OwnerSecurityProof{RequireMFA: p.ForceMFA, ExternalMFA: !p.LocalOnly, NoGrace: p.Grace == "" || p.Grace == "0s", Sequence: p.Details.Sequence}, nil
}
