package onboarding

import (
	"context"

	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

// ZitadelIdentity adapts the management client to the Identity this service
// needs; only the member listing differs in shape.
type ZitadelIdentity struct {
	*zitadel.Client
}

var _ Identity = (*ZitadelIdentity)(nil)

func (z ZitadelIdentity) OrgMembers(ctx context.Context, orgID string) ([]Member, error) {
	found, err := z.ListOrgMembers(ctx, orgID)
	if err != nil {
		return nil, err
	}
	members := make([]Member, 0, len(found))
	for _, m := range found {
		members = append(members, Member{
			UserID: m.UserID, Email: m.Email, DisplayName: m.DisplayName, Roles: m.Roles,
		})
	}
	return members, nil
}
