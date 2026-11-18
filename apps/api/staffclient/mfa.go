package staffclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/tesserix/onboarding-service/api/customercontract"
)

func (c *Client) mfaScope(s Scope, in customercontract.StaffMFARecoveryRequest) bool {
	return c != nil && c.product == "helivanta" && s.valid() && in.Validate() == nil &&
		in.Target.TenantID == s.TenantID.String() && in.Intent.CommandID == s.CommandID.String() && in.ActorSubject == s.ActorSubject
}

func (c *Client) mfaRequest(ctx context.Context, s Scope, method, suffix string, in, out any) error {
	if c == nil || c.product != "helivanta" || !s.valid() {
		return ErrInvalid
	}
	var body []byte
	var err error
	if in != nil {
		body, err = json.Marshal(in)
		if err != nil || len(body) > 8192 {
			return ErrInvalid
		}
	}
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" || len(token) > 32768 || strings.ContainsAny(token, "\r\n") {
		return ErrUnavailable
	}
	path := c.endpoint + "/v1/orgs/" + s.TenantID.String() + "/staff-mfa-recoveries/" + s.CommandID.String() + suffix
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Expected-Product-Key", c.product)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusBadRequest:
		return ErrInvalid
	case http.StatusConflict:
		return ErrConflict
	case http.StatusOK, http.StatusAccepted:
	default:
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 32769))
	if err != nil || len(data) > 32768 {
		return ErrProtocol
	}
	if json.Unmarshal(data, out) != nil {
		return ErrProtocol
	}
	return nil
}

// PrepareMFA retains an exact reviewed target without removing a native factor.
func (c *Client) PrepareMFA(ctx context.Context, s Scope, in customercontract.StaffMFARecoveryRequest) (customercontract.StaffMFARecoveryReceipt, error) {
	if !c.mfaScope(s, in) {
		return customercontract.StaffMFARecoveryReceipt{}, ErrInvalid
	}
	return c.mfaReceipt(ctx, s, http.MethodPut, "", in)
}

func (c *Client) mfaReceipt(ctx context.Context, s Scope, method, suffix string, in any) (customercontract.StaffMFARecoveryReceipt, error) {
	var out customercontract.StaffMFARecoveryReceipt
	if err := c.mfaRequest(ctx, s, method, suffix, in, &out); err != nil {
		return customercontract.StaffMFARecoveryReceipt{}, err
	}
	if out.Validate() != nil || out.CommandID != s.CommandID.String() {
		return customercontract.StaffMFARecoveryReceipt{}, ErrProtocol
	}
	return out, nil
}

// DispatchMFA asserts a committed application fence and never retries the write.
func (c *Client) DispatchMFA(ctx context.Context, s Scope, in customercontract.StaffMFADispatchRequest) (customercontract.StaffMFARecoveryReceipt, error) {
	if !c.mfaScope(s, in.Request) || in.Validate() != nil {
		return customercontract.StaffMFARecoveryReceipt{}, ErrInvalid
	}
	return c.mfaReceipt(ctx, s, http.MethodPost, "/dispatch", in)
}

// CancelMFA cancels only the exact retained intent permitted by the owner.
func (c *Client) CancelMFA(ctx context.Context, s Scope, in customercontract.StaffMFARecoveryRequest) (customercontract.StaffMFARecoveryReceipt, error) {
	if !c.mfaScope(s, in) {
		return customercontract.StaffMFARecoveryReceipt{}, ErrInvalid
	}
	return c.mfaReceipt(ctx, s, http.MethodPost, "/cancel", in)
}

// GetMFA reads private retry state; its original request must not reach a browser.
func (c *Client) GetMFA(ctx context.Context, s Scope) (customercontract.StaffMFARecoverySnapshot, error) {
	var out customercontract.StaffMFARecoverySnapshot
	query := url.Values{"actor_subject": {s.ActorSubject}}
	if err := c.mfaRequest(ctx, s, http.MethodGet, "?"+query.Encode(), nil, &out); err != nil {
		return customercontract.StaffMFARecoverySnapshot{}, err
	}
	if out.Validate() != nil || !c.mfaScope(s, out.Request) {
		return customercontract.StaffMFARecoverySnapshot{}, ErrProtocol
	}
	return out, nil
}
