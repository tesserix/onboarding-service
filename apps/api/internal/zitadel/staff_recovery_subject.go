package zitadel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"
)

// StaffRecoverySubjectProof classifies only the frozen account, never contact availability.
type StaffRecoverySubjectProof struct {
	UserID, OrganizationID, Disposition, ObjectRevision string
	CreatedAt, VerifiedAt                               time.Time
}

func (StaffRecoverySubjectProof) String() string { return "[redacted staff recovery subject proof]" }

var staffRecoveryBirthEvents = []string{"user.human.added", "user.human.selfregistered", "user.added", "user.selfregistered", "user.machine.added"}

var staffRecoveryOwnershipEvents = append(append([]string{}, staffRecoveryBirthEvents...),
	"user.metadata.set", "user.metadata.removed", "user.metadata.removed.all", "user.removed")

// InspectStaffRecoverySubject uses exact scoped history as well as source projections.
func (c *Client) InspectStaffRecoverySubject(ctx context.Context, in StaffIdentityInput, preparedAt time.Time) (StaffRecoverySubjectProof, error) {
	empty := StaffRecoverySubjectProof{}
	if err := in.Validate(); err != nil {
		return empty, err
	}
	if preparedAt.IsZero() || preparedAt.After(time.Now().Add(StaffIdentityClockSkew)) {
		return empty, ErrStaffIdentityInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return empty, ErrIdentityUnavailable
	}
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	head, err := c.staffRecoverySubjectHead(ctx, &client, token, in)
	if err != nil {
		return empty, ErrIdentityUnavailable
	}
	proof := StaffRecoverySubjectProof{UserID: in.UserID, OrganizationID: in.OrganizationID, ObjectRevision: head.Sequence}
	if head.Sequence == "" {
		if err := c.confirmStaffRecoveryMissing(ctx, &client, token, in); err != nil {
			return empty, ErrIdentityUnavailable
		}
		proof.Disposition = "absent"
	} else {
		n, _ := strconv.ParseUint(head.Sequence, 10, 64)
		if n == ^uint64(0) {
			return empty, ErrIdentityUnavailable
		}
		births, err := c.staffRecoverySubjectEvents(ctx, &client, token, in, staffRecoveryBirthEvents, n+1, 1)
		if err != nil || len(births) != 1 || births[0].Type.Type == "user.machine.added" || births[0].Payload.Email != in.Email {
			return empty, ErrIdentityUnavailable
		}
		born, err := time.Parse(time.RFC3339Nano, births[0].CreationDate)
		if err != nil || born.IsZero() || born.Before(preparedAt.Add(-StaffIdentityClockSkew)) || born.After(time.Now().Add(StaffIdentityClockSkew)) {
			return empty, ErrIdentityUnavailable
		}
		observation, err := c.ObserveStaffIdentity(ctx, in, born)
		if errors.Is(err, ErrIdentityMissing) && head.Type.Type == "user.removed" {
			if err := c.confirmStaffRecoveryMissing(ctx, &client, token, in); err != nil {
				return empty, err
			}
			if err := c.staffRecoveryRemovedOwnership(ctx, &client, token, in, head, births[0]); err != nil {
				return empty, err
			}
			proof.Disposition = "removed"
		} else {
			if err != nil || observation.UserID != in.UserID || observation.OrganizationID != in.OrganizationID || !observation.CreatedAt.Equal(born) {
				return empty, ErrIdentityUnavailable
			}
			proof.Disposition = "matching"
		}
		proof.CreatedAt = born.UTC()
	}
	final, err := c.staffRecoverySubjectHead(ctx, &client, token, in)
	if err != nil || final.Sequence != head.Sequence || ctx.Err() != nil {
		return empty, ErrIdentityUnavailable
	}
	proof.VerifiedAt = time.Now().UTC()
	return proof, nil
}

func (c *Client) staffRecoveryRemovedOwnership(ctx context.Context, client *http.Client, token string, in StaffIdentityInput, head, birth staffIdentityEvent) error {
	removed, _ := strconv.ParseUint(head.Sequence, 10, 64)
	born, _ := strconv.ParseUint(birth.Sequence, 10, 64)
	events, err := c.staffRecoverySubjectEvents(ctx, client, token, in, staffRecoveryOwnershipEvents, removed+1, 64)
	if err != nil || len(events) == 0 || events[0].Sequence != head.Sequence || born >= removed {
		return ErrIdentityUnavailable
	}
	births := 0
	var binding uint64
	for _, event := range events {
		switch event.Type.Type {
		case "user.human.added", "user.human.selfregistered", "user.added", "user.selfregistered", "user.machine.added":
			births++
			if event.Sequence != birth.Sequence {
				return ErrIdentityUnavailable
			}
		case "user.metadata.set", "user.metadata.removed":
			if event.Payload.Key == "" {
				return ErrIdentityUnavailable
			}
			if event.Payload.Key == "onboarding_staff_command" && binding == 0 {
				value, err := base64.StdEncoding.DecodeString(event.Payload.Value)
				if event.Type.Type != "user.metadata.set" || err != nil || string(value) != in.BindingNonce {
					return ErrIdentityUnavailable
				}
				binding, _ = strconv.ParseUint(event.Sequence, 10, 64)
			}
		case "user.metadata.removed.all":
			if binding == 0 {
				return ErrIdentityUnavailable
			}
		}
	}
	if births != 1 || binding <= born || binding >= removed {
		return ErrIdentityUnavailable
	}
	return nil
}

func (c *Client) confirmStaffRecoveryMissing(ctx context.Context, client *http.Client, token string, in StaffIdentityInput) error {
	for range 2 {
		var body json.RawMessage
		if err := c.readIdentity(ctx, client, token, http.MethodGet, "/v2/users/"+in.UserID, in.OrganizationID, nil, &body); !errors.Is(err, ErrIdentityMissing) {
			return ErrIdentityUnavailable
		}
	}
	return nil
}

func (c *Client) staffRecoverySubjectEvents(ctx context.Context, client *http.Client, token string, in StaffIdentityInput, types []string, upper uint64, limit int) ([]staffIdentityEvent, error) {
	var body *struct{ Events []staffIdentityEvent }
	err := c.readIdentity(ctx, client, token, http.MethodPost, "/admin/v1/events/_search", in.OrganizationID,
		map[string]any{"aggregateId": in.UserID, "aggregateTypes": []string{"user"}, "resourceOwner": in.OrganizationID,
			"eventTypes": types, "sequence": strconv.FormatUint(upper, 10), "asc": false, "limit": limit}, &body)
	if err != nil || body == nil || len(body.Events) > limit || (limit > 1 && len(body.Events) == limit) {
		return nil, ErrIdentityUnavailable
	}
	previous := upper
	for _, e := range body.Events {
		n, err := strconv.ParseUint(e.Sequence, 10, 64)
		if err != nil || !validIdentityRevision(e.Sequence) || (previous != 0 && n >= previous) || e.Aggregate.ID != in.UserID || e.Aggregate.ResourceOwner != in.OrganizationID || e.Aggregate.Type.Type != "user" || e.Type.Type == "" || len(e.Type.Type) > 200 || (len(types) > 0 && !slices.Contains(types, e.Type.Type)) {
			return nil, ErrIdentityUnavailable
		}
		previous = n
	}
	return body.Events, nil
}

func (c *Client) staffRecoverySubjectHead(ctx context.Context, client *http.Client, token string, in StaffIdentityInput) (staffIdentityEvent, error) {
	events, err := c.staffRecoverySubjectEvents(ctx, client, token, in, nil, 0, 1)
	if err != nil {
		return staffIdentityEvent{}, err
	}
	if len(events) == 0 {
		return staffIdentityEvent{}, nil
	}
	return events[0], nil
}
