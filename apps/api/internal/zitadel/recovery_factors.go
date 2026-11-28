package zitadel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FactorRemovalEvidence is the last native acknowledgement of a recovery
// removal: the highest sequence, so later enrolment must come after it.
type FactorRemovalEvidence struct {
	Sequence  string
	ChangedAt time.Time
}

// FactorEnrollmentEvidence names the kind ("totp" or "security_key") of a
// verified enrolment so the session proof can require that same factor.
type FactorEnrollmentEvidence struct {
	Verified   bool
	Kind       string
	Sequence   string
	VerifiedAt time.Time
}

const recoveryFactorLimit = 20

var recoveryFactorEventKinds = map[string]string{
	"user.human.mfa.otp.added": "totp", "user.human.mfa.otp.verified": "totp", "user.human.mfa.otp.removed": "totp",
	"user.human.mfa.u2f.token.added": "security_key", "user.human.mfa.u2f.token.verified": "security_key",
	"user.human.mfa.u2f.token.removed": "security_key",
}

// RemoveRecoveryFactors is only for a durably claimed, authorized recovery
// dispatch. It removes every security key (any state) and the TOTP, then
// re-reads presence. A failed or lost acknowledgement at any step makes the
// whole removal uncertain, and it must never be retried: a partial removal
// is never evidence (#1905).
func (c *Client) RemoveRecoveryFactors(ctx context.Context, org, subject string) (FactorRemovalEvidence, error) {
	var empty FactorRemovalEvidence
	if c == nil || c.tokens == nil || c.http == nil || !identitySourceID.MatchString(org) || !identitySourceID.MatchString(subject) {
		return empty, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return empty, ErrIdentityUnavailable
	}
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	keys, totp, err := c.recoveryFactors(ctx, &client, token, org, subject)
	if err != nil {
		return empty, err
	}
	paths := make([]string, 0, len(keys)+1)
	for _, key := range keys {
		paths = append(paths, "/v2/users/"+subject+"/u2f/"+key)
	}
	if totp {
		paths = append(paths, "/v2/users/"+subject+"/totp")
	}
	if len(paths) == 0 {
		return empty, ErrIdentityUnavailable
	}
	var last FactorRemovalEvidence
	var previous uint64
	for _, path := range paths {
		ack, err := c.removeRecoveryFactor(ctx, &client, token, org, path)
		if err != nil {
			return empty, err
		}
		sequence, _ := strconv.ParseUint(ack.Sequence, 10, 64)
		if sequence <= previous || ack.ChangedAt.Before(last.ChangedAt) {
			return empty, ErrIdentityUnavailable
		}
		previous, last = sequence, ack
	}
	methods, err := c.authenticationMethods(ctx, org, subject)
	if err != nil || methods["AUTHENTICATION_METHOD_TYPE_TOTP"] || methods["AUTHENTICATION_METHOD_TYPE_U2F"] {
		return empty, ErrIdentityUnavailable
	}
	return last, nil
}

// recoveryFactors lists the user's security key ids and whether a TOTP is
// registered (POST /management/v1/users/{id}/auth_factors/_search). protojson
// omits an empty list, so no result is no factors.
func (c *Client) recoveryFactors(ctx context.Context, client *http.Client, token, org, subject string) ([]string, bool, error) {
	var raw json.RawMessage
	if c.readIdentity(ctx, client, token, http.MethodPost, "/management/v1/users/"+subject+"/auth_factors/_search", org, map[string]any{}, &raw) != nil {
		return nil, false, ErrIdentityUnavailable
	}
	root, err := identityObject(raw, "result")
	if err != nil {
		return nil, false, ErrIdentityUnavailable
	}
	var entries []json.RawMessage
	if len(root["result"]) != 0 && json.Unmarshal(root["result"], &entries) != nil || len(entries) > recoveryFactorLimit {
		return nil, false, ErrIdentityUnavailable
	}
	keys, seen, totp := []string{}, map[string]bool{}, false
	for _, entry := range entries {
		factor, err := identityObject(entry, "state", "otp", "u2f")
		if err != nil {
			return nil, false, ErrIdentityUnavailable
		}
		_, isTOTP := factor["otp"]
		u2f, isKey := factor["u2f"]
		switch {
		case isTOTP && !isKey:
			totp = true
		case isKey && !isTOTP:
			key, err := identityObject(u2f, "id")
			if err != nil {
				return nil, false, ErrIdentityUnavailable
			}
			var id string
			if json.Unmarshal(key["id"], &id) != nil || !identitySourceID.MatchString(id) || seen[id] {
				return nil, false, ErrIdentityUnavailable
			}
			seen[id] = true
			keys = append(keys, id)
		}
		// Other kinds (OTP email/SMS, passkeys) are not collected or removed (spec: not covered).
	}
	return keys, totp, nil
}

func (c *Client) removeRecoveryFactor(ctx context.Context, client *http.Client, token, org, path string) (FactorRemovalEvidence, error) {
	var empty FactorRemovalEvidence
	var raw json.RawMessage
	err := c.readIdentity(ctx, client, token, http.MethodDelete, path, org, nil, &raw)
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
	return FactorRemovalEvidence{Sequence: acknowledgement.Sequence, ChangedAt: acknowledgement.ChangeDate}, nil
}

// RecoveryFactorEnrollment reads the latest TOTP or security-key lifecycle
// event, not projected presence or ordinary factor-check activity (a key's
// sign-count event is not searched). No factor secret is returned or retained.
func (c *Client) RecoveryFactorEnrollment(ctx context.Context, org, subject string, removal FactorRemovalEvidence) (_ FactorEnrollmentEvidence, resultErr error) {
	stage := "configuration"
	defer func() {
		if c != nil && c.log != nil && errors.Is(resultErr, ErrIdentityUnavailable) {
			c.log.Warn("Staff MFA enrollment proof unavailable", "stage", stage)
		}
	}()
	var empty FactorEnrollmentEvidence
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
			"eventTypes": recoveryFactorEventTypes(), "asc": false, "limit": 1}, &raw)
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
		(sequence == baseline && (!strings.HasSuffix(event.Type.Type, ".removed") || !changed.Equal(removal.ChangedAt))) {
		return empty, ErrIdentityUnavailable
	}
	kind, known := recoveryFactorEventKinds[event.Type.Type]
	switch {
	case !known:
		return empty, ErrIdentityUnavailable
	case strings.HasSuffix(event.Type.Type, ".verified"):
		return FactorEnrollmentEvidence{Verified: true, Kind: kind, Sequence: event.Sequence, VerifiedAt: changed}, nil
	default:
		return FactorEnrollmentEvidence{Sequence: event.Sequence}, nil
	}
}

func recoveryFactorEventTypes() []string {
	types := make([]string, 0, len(recoveryFactorEventKinds))
	for name := range recoveryFactorEventKinds {
		types = append(types, name)
	}
	sort.Strings(types)
	return types
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
