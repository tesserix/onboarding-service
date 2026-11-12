package zitadel

import (
	"context"
	"net/http"
	"slices"
	"strconv"
)

func (c *Client) latestStaffIdentityEvent(ctx context.Context, client *http.Client, token string, in StaffIdentityInput, types []string) (staffIdentityEvent, error) {
	empty := staffIdentityEvent{}
	var body *struct{ Events []staffIdentityEvent }
	err := c.readIdentity(ctx, client, token, http.MethodPost, "/admin/v1/events/_search", in.OrganizationID,
		map[string]any{"aggregateId": in.UserID, "aggregateTypes": []string{"user"}, "resourceOwner": in.OrganizationID,
			"eventTypes": types, "sequence": "0", "asc": false, "limit": 1}, &body)
	if err != nil || body == nil || ctx.Err() != nil || len(body.Events) > 1 {
		return empty, ErrIdentityUnavailable
	}
	if len(body.Events) == 0 {
		return empty, nil
	}
	event := body.Events[0]
	if !validIdentityRevision(event.Sequence) || event.Aggregate.ID != in.UserID || event.Aggregate.ResourceOwner != in.OrganizationID || event.Aggregate.Type.Type != "user" || !slices.Contains(types, event.Type.Type) {
		return empty, ErrIdentityUnavailable
	}
	return event, nil
}

func (c *Client) checkStaffIdentityCurrent(ctx context.Context, client *http.Client, token string, in StaffIdentityInput, proof staffUserProof) error {
	// A projection may skip a failed mutation, then advance on an unrelated field.
	state, err := c.latestStaffIdentityEvent(ctx, client, token, in, staffIdentityEventTypes[:9])
	if err != nil {
		return err
	}
	expectedState := "active"
	switch state.Type.Type {
	case "user.locked":
		expectedState = "locked"
	case "user.deactivated":
		expectedState = "inactive"
	case "user.removed":
		expectedState = "deleted"
	case "user.human.initialization.code.added", "user.initialization.code.added":
		expectedState = "initial"
	}
	if expectedState != proof.state {
		return ErrIdentityUnavailable
	}
	changed, err := c.latestStaffIdentityEvent(ctx, client, token, in, []string{"user.human.email.changed", "user.email.changed"})
	if err != nil {
		return err
	}
	if changed.Sequence != "" && changed.Payload.Email != in.Email {
		return ErrIdentityUnavailable
	}
	verified, err := c.latestStaffIdentityEvent(ctx, client, token, in, []string{"user.human.email.verified", "user.email.verified"})
	if err != nil {
		return err
	}
	var changedAt, verifiedAt uint64
	if changed.Sequence != "" {
		changedAt, err = strconv.ParseUint(changed.Sequence, 10, 64)
		if err != nil {
			return ErrIdentityUnavailable
		}
	}
	if verified.Sequence != "" {
		verifiedAt, err = strconv.ParseUint(verified.Sequence, 10, 64)
		if err != nil {
			return ErrIdentityUnavailable
		}
	}
	if proof.verified != (verifiedAt > changedAt) {
		return ErrIdentityUnavailable
	}
	return nil
}
