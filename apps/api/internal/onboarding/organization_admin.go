package onboarding

import (
	"context"
	"time"
)

// IsOrganizationOwner checks live management membership, independently of login claims.
func (s *Service) IsOrganizationOwner(ctx context.Context, orgID, subject string) (bool, error) {
	if orgID == "" || subject == "" {
		return false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	members, err := s.idp.OrgMembers(ctx, orgID)
	if err != nil {
		return false, err
	}
	for _, member := range members {
		if member.UserID != subject {
			continue
		}
		for _, role := range member.Roles {
			if role == "ORG_OWNER" {
				return true, nil
			}
		}
	}
	return false, nil
}
