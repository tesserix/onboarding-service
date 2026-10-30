package zitadel

import (
	"context"
	"net/http"
	"strconv"
)

const staffIdentityEventLimit = 32

// v4.15.3 projection reducers: user.go and user_metadata.go; ADR-0004.
var staffIdentityEventTypes = []string{
	"user.locked", "user.unlocked", "user.deactivated", "user.reactivated", "user.removed",
	"user.human.initialization.code.added", "user.human.initialization.check.succeeded",
	"user.initialization.code.added", "user.initialization.check.succeeded",
	"user.human.email.changed", "user.human.email.verified", "user.email.changed", "user.email.verified",
	"user.metadata.set", "user.metadata.removed", "user.metadata.removed.all",
}

type staffIdentityEvent struct {
	Sequence  string
	Aggregate struct {
		ID, ResourceOwner string
		Type              struct{ Type string }
	}
	Type    struct{ Type string }
	Payload struct{ Key, Email string }
}

func (c *Client) checkStaffIdentityEvents(ctx context.Context, client *http.Client, token string, in StaffIdentityInput, proof staffUserProof, metadataRevision string) error {
	userSequence, err := strconv.ParseUint(proof.revision, 10, 64)
	if err != nil {
		return ErrIdentityUnavailable
	}
	metadataSequence, err := strconv.ParseUint(metadataRevision, 10, 64)
	if err != nil {
		return ErrIdentityUnavailable
	}
	sequence := min(userSequence, metadataSequence)
	var body *struct {
		Events []staffIdentityEvent
	}
	err = c.readIdentity(ctx, client, token, http.MethodPost, "/admin/v1/events/_search", in.OrganizationID,
		map[string]any{"aggregateId": in.UserID, "aggregateTypes": []string{"user"},
			"resourceOwner": in.OrganizationID, "eventTypes": staffIdentityEventTypes,
			"sequence": strconv.FormatUint(sequence, 10), "asc": true, "limit": staffIdentityEventLimit}, &body)
	if err != nil || body == nil || ctx.Err() != nil || len(body.Events) >= staffIdentityEventLimit {
		return ErrIdentityUnavailable
	}
	initialState := ""
	for _, event := range body.Events {
		n, err := strconv.ParseUint(event.Sequence, 10, 64)
		if err != nil || !validIdentityRevision(event.Sequence) || n <= sequence ||
			event.Aggregate.ID != in.UserID || event.Aggregate.ResourceOwner != in.OrganizationID || event.Aggregate.Type.Type != "user" {
			return ErrIdentityUnavailable
		}
		sequence = n
		switch event.Type.Type {
		case "user.human.initialization.code.added", "user.initialization.code.added":
			if n > userSequence {
				initialState = "initial"
			}
		case "user.human.initialization.check.succeeded", "user.initialization.check.succeeded":
			if n > userSequence {
				initialState = "active"
			}
		case "user.locked", "user.unlocked", "user.deactivated", "user.reactivated", "user.removed",
			"user.human.email.changed", "user.human.email.verified", "user.email.changed", "user.email.verified":
			if n > userSequence {
				return ErrIdentityUnavailable
			}
		case "user.metadata.set", "user.metadata.removed":
			if event.Payload.Key == "" || (event.Payload.Key == "onboarding_staff_command" && n > metadataSequence) {
				return ErrIdentityUnavailable
			}
		case "user.metadata.removed.all":
			if n > metadataSequence {
				return ErrIdentityUnavailable
			}
		default:
			return ErrIdentityUnavailable
		}
	}
	if initialState != "" && initialState != proof.state {
		return ErrIdentityUnavailable
	}
	return nil
}
