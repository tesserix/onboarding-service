package zitadel

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

// VerifyExistingHuman keeps product onboarding bound to an existing verified person.
func (c *Client) VerifyExistingHuman(ctx context.Context, userID, email string) error {
	if !identitySourceID.MatchString(userID) || email == "" {
		return ErrIdentityUnavailable
	}
	var out struct {
		User struct {
			ID    string `json:"userId"`
			State string `json:"state"`
			Human *struct {
				Email struct {
					Address  string `json:"email"`
					Verified bool   `json:"isVerified"`
				} `json:"email"`
			} `json:"human"`
		} `json:"user"`
	}
	if err := c.do(ctx, http.MethodGet, "/v2/users/"+userID, "", nil, &out); err != nil {
		return ErrIdentityUnavailable
	}
	u := out.User
	if u.ID != userID || u.State != "USER_STATE_ACTIVE" || u.Human == nil || !u.Human.Email.Verified || !strings.EqualFold(u.Human.Email.Address, email) {
		return fmt.Errorf("%w: existing owner is not an active verified human", ErrIdentityUnavailable)
	}
	return nil
}
