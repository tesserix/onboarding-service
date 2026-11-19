package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

var ErrRecoveryPasswordPolicy = errors.New("native recovery password policy refused")

// PasswordChangeEvidence is the minimized acknowledgement of a native password write.
type PasswordChangeEvidence struct {
	Sequence  string
	ChangedAt time.Time
}

// SetRecoveryPassword requires native reset proof and checks the acknowledged organization.
func (c *Client) SetRecoveryPassword(ctx context.Context, org, subject, code, password string) (PasswordChangeEvidence, error) {
	var empty PasswordChangeEvidence
	if !identitySourceID.MatchString(org) || !validSignupCode(subject, code) || password == "" || !utf8.ValidString(password) || utf8.RuneCountInString(password) > 200 {
		return empty, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return empty, ErrIdentityUnavailable
	}
	raw, err := json.Marshal(map[string]any{"verificationCode": code, "newPassword": map[string]any{"password": password, "changeRequired": false}})
	if err != nil {
		return empty, ErrIdentityUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v2/users/"+subject+"/password", bytes.NewReader(raw))
	if err != nil {
		return empty, ErrIdentityUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Zitadel-Orgid", org)
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return empty, ErrIdentityUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, identityReadLimit+1))
	if err != nil || len(body) > identityReadLimit {
		return empty, ErrIdentityUnavailable
	}
	if response.StatusCode == http.StatusBadRequest && recoveryPolicyRefusal(body) {
		return empty, ErrRecoveryPasswordPolicy
	}
	if response.StatusCode != http.StatusOK {
		return empty, ErrIdentityUnavailable
	}
	var wire struct {
		Details struct {
			Sequence string `json:"sequence"`
			Owner    string `json:"resourceOwner"`
			Changed  string `json:"changeDate"`
		} `json:"details"`
	}
	if decodeIdentityJSON(body, &wire) != nil {
		return empty, ErrIdentityUnavailable
	}
	root, err := identityObject(body, "details")
	if err != nil {
		return empty, ErrIdentityUnavailable
	}
	if _, err = identityObject(root["details"], "sequence", "resourceOwner", "changeDate"); err != nil {
		return empty, ErrIdentityUnavailable
	}
	changed, err := time.Parse(time.RFC3339Nano, wire.Details.Changed)
	if err != nil || !changed.After(time.Unix(0, 0)) || wire.Details.Owner != org || !validIdentityRevision(wire.Details.Sequence) {
		return empty, ErrIdentityUnavailable
	}
	return PasswordChangeEvidence{Sequence: wire.Details.Sequence, ChangedAt: changed.UTC()}, nil
}

func recoveryPolicyRefusal(raw []byte) bool {
	var response struct {
		Code    int `json:"code"`
		Details []struct {
			Type string `json:"@type"`
			ID   string `json:"id"`
		} `json:"details"`
	}
	if decodeIdentityJSON(raw, &response) != nil || response.Code != 3 || len(response.Details) != 1 || response.Details[0].Type != "type.googleapis.com/zitadel.v1.ErrorDetail" {
		return false
	}
	switch response.Details[0].ID {
	case "DOMAIN-HuJf6", "DOMAIN-co3Xw", "DOMAIN-VoaRj", "DOMAIN-ZBv4H", "DOMAIN-ZDLwA":
		return true
	default:
		return false
	}
}

// RecoveryCredentialState verifies ownership/mailbox and reuses canonical native cutoff parsing.
func (c *Client) RecoveryCredentialState(ctx context.Context, org, subject, email string) (customercontract.IdentityCredentialState, error) {
	var empty customercontract.IdentityCredentialState
	if !identitySourceID.MatchString(org) || !identitySourceID.MatchString(subject) || (customercontract.DiscoveryRequest{Email: email}).Validate() != nil {
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
	if err = c.readIdentity(ctx, &client, token, http.MethodGet, "/v2/users/"+subject, org, nil, &raw); err != nil {
		return empty, err
	}
	base := customercontract.IdentityProjection{TenantID: strings.TrimPrefix(org, "helivanta-"), OrganizationID: org, Subject: subject, Purpose: "membership", State: "inactive", Admission: "ineligible"}
	view, err := nativeIdentityProjection(raw, base)
	if err != nil {
		return empty, err
	}
	if view.State != "active" || view.Admission != "eligible" {
		return empty, ErrIdentityMissing
	}
	var user struct {
		User struct {
			Human struct {
				Email struct {
					Address string `json:"email"`
				} `json:"email"`
			} `json:"human"`
		} `json:"user"`
	}
	if json.Unmarshal(raw, &user) != nil || !strings.EqualFold(user.User.Human.Email.Address, email) {
		return empty, ErrIdentityMissing
	}
	view.Purpose = "status"
	view.Admission = "unassessed"
	view, err = c.identityCredentials(ctx, subject, raw, view)
	if err != nil || view.Credentials == nil {
		return empty, ErrIdentityUnavailable
	}
	return *view.Credentials, nil
}
