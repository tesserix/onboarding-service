package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tesserix/onboarding-service/api/customercontract"
)

var ErrRecoveryTargetIneligible = errors.New("native account is not an ordinary staff recovery target")

// CheckStaffMFARecoveryTarget protects the native boundary. IAM remains responsible
// for current administrator/ordinary-target authority and Workforce for the link.
func (c *Client) CheckStaffMFARecoveryTarget(ctx context.Context, org, subject string) error {
	base := customercontract.IdentityProjection{TenantID: strings.TrimPrefix(org, "helivanta-"), OrganizationID: org, Subject: subject, State: "inactive", Admission: "ineligible", Purpose: "membership"}
	if c == nil || c.tokens == nil || c.http == nil || base.Validate() != nil {
		return ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return ErrIdentityUnavailable
	}
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var raw json.RawMessage
	err = c.readIdentity(ctx, &client, token, http.MethodGet, "/v2/users/"+subject, org, nil, &raw)
	if errors.Is(err, ErrIdentityMissing) {
		return ErrRecoveryTargetIneligible
	}
	if err != nil {
		return ErrIdentityUnavailable
	}
	account, err := nativeIdentityProjection(raw, base)
	if err != nil {
		return ErrIdentityUnavailable
	}
	if account.State != "active" || account.Admission != "eligible" {
		return ErrRecoveryTargetIneligible
	}
	if err = recoveryTargetPassword(raw); err != nil {
		return err
	}
	var members json.RawMessage
	err = c.readIdentity(ctx, &client, token, http.MethodPost, "/management/v1/orgs/me/members/_search", org,
		map[string]any{"query": map[string]any{"limit": 1}, "queries": []any{map[string]any{"userIdQuery": map[string]string{"userId": subject}}}}, &members)
	if err != nil || ctx.Err() != nil {
		return ErrIdentityUnavailable
	}
	return ordinaryRecoveryOrgMember(members, subject)
}

func ordinaryRecoveryOrgMember(raw []byte, subject string) error {
	root, err := identityObject(raw, "details", "result")
	if err != nil {
		return ErrIdentityUnavailable
	}
	details, err := identityObject(root["details"], "totalResult", "processedSequence", "viewTimestamp")
	if err != nil {
		return ErrIdentityUnavailable
	}
	var count string
	sequence := "0"
	if value, present := details["processedSequence"]; present {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &sequence) != nil || sequence != "0" && !validIdentityRevision(sequence) {
			return ErrIdentityUnavailable
		}
	}
	requiresTimestamp := sequence == "0"
	if value, present := details["totalResult"]; present {
		if json.Unmarshal(value, &count) != nil {
			return ErrIdentityUnavailable
		}
	} else {
		requiresTimestamp = true
		count = "0"
	}
	n, err := strconv.ParseUint(count, 10, 64)
	if err != nil || strconv.FormatUint(n, 10) != count || n > 1 || sequence == "0" && n != 0 {
		return ErrIdentityUnavailable
	}
	// ProtoJSON omits zero counters in a timestamped empty native projection.
	if requiresTimestamp {
		var timestamp string
		if json.Unmarshal(details["viewTimestamp"], &timestamp) != nil {
			return ErrIdentityUnavailable
		}
		at, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil || !recoverySessionInstant(at, time.Now()) {
			return ErrIdentityUnavailable
		}
	}
	var result []json.RawMessage
	if value, present := root["result"]; present {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &result) != nil {
			return ErrIdentityUnavailable
		}
	}
	if uint64(len(result)) != n {
		return ErrIdentityUnavailable
	}
	if n == 0 {
		return nil
	}
	member, err := identityObject(result[0], "userId", "roles")
	if err != nil {
		return ErrIdentityUnavailable
	}
	var id string
	var roles []string
	if json.Unmarshal(member["userId"], &id) != nil || id != subject || json.Unmarshal(member["roles"], &roles) != nil || len(roles) == 0 || len(roles) > 64 {
		return ErrIdentityUnavailable
	}
	for _, role := range roles {
		if role == "" || len(role) > 100 || strings.TrimSpace(role) != role {
			return ErrIdentityUnavailable
		}
	}
	return ErrRecoveryTargetIneligible
}

func recoveryTargetPassword(raw []byte) error {
	root, err := identityObject(raw, "user")
	if err != nil {
		return ErrIdentityUnavailable
	}
	user, err := identityObject(root["user"], "human")
	if err != nil {
		return ErrIdentityUnavailable
	}
	human, err := identityObject(user["human"], "passwordChanged")
	if err != nil {
		return ErrIdentityUnavailable
	}
	value, present := human["passwordChanged"]
	if !present {
		return ErrRecoveryTargetIneligible
	}
	var timestamp string
	if json.Unmarshal(value, &timestamp) != nil {
		return ErrIdentityUnavailable
	}
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil || !recoverySessionInstant(at, time.Now()) {
		return ErrIdentityUnavailable
	}
	return nil
}
