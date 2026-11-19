package zitadel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"
)

type TOTPRemovalEvidence struct {
	Sequence  string
	ChangedAt time.Time
}

type TOTPEnrollmentEvidence struct {
	Verified   bool
	Sequence   string
	VerifiedAt time.Time
}

var recoveryTOTPEventTypes = []string{
	"user.human.mfa.otp.added", "user.human.mfa.otp.verified", "user.human.mfa.otp.removed",
}

// RemoveRecoveryTOTP is only for a durably claimed, authorized recovery dispatch.
// A failed or lost acknowledgement is uncertain and must never be retried.
func (c *Client) RemoveRecoveryTOTP(ctx context.Context, org, subject string) (TOTPRemovalEvidence, error) {
	var empty TOTPRemovalEvidence
	if !identitySourceID.MatchString(org) || !identitySourceID.MatchString(subject) {
		return empty, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return empty, ErrIdentityUnavailable
	}
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var raw json.RawMessage
	err = c.readIdentity(ctx, &client, token, http.MethodDelete, "/v2/users/"+subject+"/totp", org, nil, &raw)
	if err != nil || ctx.Err() != nil {
		return empty, ErrIdentityUnavailable
	}
	root, err := identityObject(raw, "details")
	if err != nil {
		return empty, ErrIdentityUnavailable
	}
	if _, err = identityObject(root["details"], "sequence", "resourceOwner", "changeDate"); err != nil {
		return empty, ErrIdentityUnavailable
	}
	var acknowledgement struct {
		Sequence      string    `json:"sequence"`
		ResourceOwner string    `json:"resourceOwner"`
		ChangeDate    time.Time `json:"changeDate"`
	}
	if json.Unmarshal(root["details"], &acknowledgement) != nil ||
		!validIdentityRevision(acknowledgement.Sequence) || acknowledgement.ResourceOwner != org ||
		!recoverySessionInstant(acknowledgement.ChangeDate, time.Now()) {
		return empty, ErrIdentityUnavailable
	}
	return TOTPRemovalEvidence{Sequence: acknowledgement.Sequence, ChangedAt: acknowledgement.ChangeDate}, nil
}

// RecoveryTOTPEnrollment reads the latest lifecycle event, not projected presence
// or ordinary factor-check activity. No factor secret is returned or retained.
func (c *Client) RecoveryTOTPEnrollment(ctx context.Context, org, subject string, removal TOTPRemovalEvidence) (_ TOTPEnrollmentEvidence, resultErr error) {
	stage := "configuration"
	defer func() {
		if c != nil && c.log != nil && errors.Is(resultErr, ErrIdentityUnavailable) {
			c.log.Warn("Staff MFA enrollment proof unavailable", "stage", stage)
		}
	}()
	var empty TOTPEnrollmentEvidence
	if !identitySourceID.MatchString(org) || !identitySourceID.MatchString(subject) ||
		!validIdentityRevision(removal.Sequence) || !recoverySessionInstant(removal.ChangedAt, time.Now()) {
		return empty, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	stage = "token"
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return empty, ErrIdentityUnavailable
	}
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var raw json.RawMessage
	stage = "read"
	err = c.readIdentity(ctx, &client, token, http.MethodPost, "/admin/v1/events/_search", org,
		map[string]any{"aggregateId": subject, "aggregateTypes": []string{"user"}, "resourceOwner": org,
			"eventTypes": recoveryTOTPEventTypes, "asc": false, "limit": 1}, &raw)
	if err != nil || ctx.Err() != nil {
		return empty, ErrIdentityUnavailable
	}
	stage = "decode"
	event, err := decodeRecoveryTOTPEvent(raw)
	if err != nil {
		return empty, err
	}
	stage = "proof"
	sequence, parseErr := strconv.ParseUint(event.Sequence, 10, 64)
	baseline, _ := strconv.ParseUint(removal.Sequence, 10, 64)
	changed, timeErr := time.Parse(time.RFC3339Nano, event.CreationDate)
	if parseErr != nil || !validIdentityRevision(event.Sequence) || sequence < baseline || timeErr != nil ||
		!recoverySessionInstant(changed, time.Now()) || changed.Before(removal.ChangedAt) ||
		event.Aggregate.ID != subject || event.Aggregate.ResourceOwner != org || event.Aggregate.Type.Type != "user" ||
		(sequence == baseline && (event.Type.Type != "user.human.mfa.otp.removed" || !changed.Equal(removal.ChangedAt))) {
		return empty, ErrIdentityUnavailable
	}
	switch event.Type.Type {
	case "user.human.mfa.otp.verified":
		return TOTPEnrollmentEvidence{Verified: true, Sequence: event.Sequence, VerifiedAt: changed}, nil
	case "user.human.mfa.otp.added", "user.human.mfa.otp.removed":
		return TOTPEnrollmentEvidence{Sequence: event.Sequence}, nil
	default:
		return empty, ErrIdentityUnavailable
	}
}

func decodeRecoveryTOTPEvent(raw []byte) (staffIdentityEvent, error) {
	var empty staffIdentityEvent
	root, err := identityObject(raw, "events")
	if err != nil {
		return empty, ErrIdentityUnavailable
	}
	var events []json.RawMessage
	if json.Unmarshal(root["events"], &events) != nil || len(events) != 1 {
		return empty, ErrIdentityUnavailable
	}
	event, err := identityObject(events[0], "sequence", "creationDate", "aggregate", "type")
	if err != nil {
		return empty, ErrIdentityUnavailable
	}
	aggregate, err := identityObject(event["aggregate"], "id", "resourceOwner", "type")
	if err != nil {
		return empty, ErrIdentityUnavailable
	}
	for _, value := range []json.RawMessage{aggregate["type"], event["type"]} {
		if _, err = identityObject(value, "type"); err != nil {
			return empty, ErrIdentityUnavailable
		}
	}
	var out staffIdentityEvent
	if json.Unmarshal(events[0], &out) != nil {
		return empty, ErrIdentityUnavailable
	}
	return out, nil
}
