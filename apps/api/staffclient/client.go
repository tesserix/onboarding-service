// Package staffclient consumes the identity owner's canonical Staff contracts.
package staffclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/staffcontract"
)

var (
	ErrInvalid     = errors.New("invalid staff identity command")
	ErrUnavailable = errors.New("staff identity owner unavailable")
	ErrProtocol    = errors.New("staff identity response could not be verified")
	ErrNotFound    = errors.New("staff identity command not found")
	ErrConflict    = errors.New("staff identity command changed or needs review")
	idPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}$`)
	productPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}$`)
)

type TokenSource interface {
	Token(context.Context) (string, error)
}
type Config struct {
	Endpoint, ProductKey string
	Tokens               TokenSource
	HTTP                 *http.Client
}

func (Config) String() string   { return "[redacted staff client config]" }
func (Config) GoString() string { return "[redacted staff client config]" }

type Client struct {
	endpoint, product string
	tokens            TokenSource
	http              *http.Client
}

func (Client) String() string   { return "[redacted staff identity client]" }
func (Client) GoString() string { return "[redacted staff identity client]" }

func New(cfg Config) (*Client, error) {
	u, err := url.Parse(cfg.Endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "onboarding-api.onboarding.svc.cluster.local" || (u.Port() != "" && u.Port() != "80" && u.Port() != "8080") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !productPattern.MatchString(cfg.ProductKey) || cfg.Tokens == nil {
		return nil, ErrInvalid
	}
	h := http.Client{Timeout: 30 * time.Second}
	if cfg.HTTP != nil {
		h = *cfg.HTTP
		h.Timeout = 30 * time.Second
	}
	h.Jar = nil
	h.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{cfg.Endpoint, cfg.ProductKey, cfg.Tokens, &h}, nil
}

type Scope struct {
	TenantID, CommandID uuid.UUID
	ActorSubject        string
}

func (Scope) String() string   { return "[redacted staff identity scope]" }
func (Scope) GoString() string { return "[redacted staff identity scope]" }
func (s Scope) valid() bool {
	return s.TenantID != uuid.Nil && s.TenantID.Variant() == uuid.RFC4122 && s.CommandID.Version() == 7 && s.CommandID.Variant() == uuid.RFC4122 && idPattern.MatchString(s.ActorSubject)
}

type RateLimitError struct{ RetryAfterSeconds int }

func (RateLimitError) Error() string { return "staff setup resend limit reached" }

func (c *Client) request(ctx context.Context, s Scope, method, suffix string, in, out any) error {
	if c == nil || !s.valid() {
		return ErrInvalid
	}
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" || len(token) > 32768 || strings.ContainsAny(token, "\r\n") {
		return ErrUnavailable
	}
	var body []byte
	if in != nil {
		body, err = json.Marshal(in)
		if err != nil || len(body) > 4096 {
			return ErrInvalid
		}
	}
	path := c.endpoint + "/v1/orgs/" + s.TenantID.String() + "/staff-identities/" + s.CommandID.String() + suffix
	request, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return ErrInvalid
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("X-Expected-Product-Key", c.product)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	switch response.StatusCode {
	case 404:
		return ErrNotFound
	case 400:
		return ErrInvalid
	case 409:
		return ErrConflict
	case 429:
		n, err := strconv.Atoi(response.Header.Get("Retry-After"))
		if err != nil || n < 1 || n > 86400 {
			return ErrUnavailable
		}
		return RateLimitError{n}
	case 200, 202:
	default:
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 32769))
	if err != nil || len(data) > 32768 || !utf8.Valid(data) {
		return ErrProtocol
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrProtocol
	}
	return nil
}

func (c *Client) receipt(s Scope, r staffcontract.StaffIdentityReceipt) error {
	if r.SchemaVersion != 1 || r.CommandID != s.CommandID || r.TenantID != s.TenantID || r.ActorSubject != s.ActorSubject || r.ProductKey != c.product || r.PreparedAt.IsZero() || r.PreparedAt.After(time.Now().Add(time.Minute)) {
		return ErrProtocol
	}
	switch r.CommandState {
	case "prepared", "dispatched":
		if r.UserID != "" || r.CreatedAt != nil {
			return ErrProtocol
		}
	case "created":
		if !idPattern.MatchString(r.UserID) || r.CreatedAt == nil || r.CreatedAt.IsZero() || r.CreatedAt.After(time.Now().Add(time.Minute)) {
			return ErrProtocol
		}
	default:
		return ErrProtocol
	}
	if r.RecoveryState != "" && r.RecoveryState != "closed_unresolved" && r.RecoveryState != "superseded" {
		return ErrProtocol
	}
	if r.RecoveryState == "superseded" {
		if r.SuccessorCommandID == nil || r.SuccessorCommandID.Version() != 7 {
			return ErrProtocol
		}
	} else if r.SuccessorCommandID != nil {
		return ErrProtocol
	}
	return nil
}

func (c *Client) Create(ctx context.Context, s Scope, in staffcontract.StaffIdentityCreateInput) (staffcontract.StaffIdentityReceipt, error) {
	var out staffcontract.StaffIdentityReceipt
	if in.ActorSubject != s.ActorSubject {
		return out, ErrInvalid
	}
	if err := c.request(ctx, s, "PUT", "", in, &out); err != nil {
		return out, err
	}
	if err := c.receipt(s, out); err != nil {
		return staffcontract.StaffIdentityReceipt{}, err
	}
	return out, nil
}

func (c *Client) Get(ctx context.Context, s Scope) (staffcontract.StaffIdentityReceipt, error) {
	var out staffcontract.StaffIdentityReceipt
	query := url.Values{"actor_subject": {s.ActorSubject}}
	if err := c.request(ctx, s, "GET", "?"+query.Encode(), nil, &out); err != nil {
		return out, err
	}
	if err := c.receipt(s, out); err != nil {
		return staffcontract.StaffIdentityReceipt{}, err
	}
	return out, nil
}

func (c *Client) Observe(ctx context.Context, s Scope) (staffcontract.StaffIdentityObservationReceipt, error) {
	var out staffcontract.StaffIdentityObservationReceipt
	started := time.Now()
	if err := c.request(ctx, s, "POST", "/observations", map[string]string{"actor_subject": s.ActorSubject}, &out); err != nil {
		return out, err
	}
	if c.receipt(s, out.Receipt) != nil || out.Receipt.CommandState != "created" || out.Receipt.RecoveryState != "" || out.Observation.ObservedAt.Before(started.Add(-30*time.Second)) || out.Observation.ObservedAt.After(time.Now().Add(time.Minute)) {
		return staffcontract.StaffIdentityObservationReceipt{}, ErrProtocol
	}
	switch out.Observation.State {
	case "active", "initial", "inactive", "locked":
		if out.Observation.UserRevision == "" || out.Observation.MetadataRevision == "" {
			return staffcontract.StaffIdentityObservationReceipt{}, ErrProtocol
		}
	case "missing", "removed":
	default:
		return staffcontract.StaffIdentityObservationReceipt{}, ErrProtocol
	}
	return out, nil
}

func (c *Client) Deliver(ctx context.Context, s Scope, id uuid.UUID) (staffcontract.StaffInvitationReceipt, error) {
	var out staffcontract.StaffInvitationReceipt
	if id.Version() != 7 || id.Variant() != uuid.RFC4122 {
		return out, ErrInvalid
	}
	if err := c.request(ctx, s, "PUT", "/invitations/"+id.String(), map[string]string{"actor_subject": s.ActorSubject}, &out); err != nil {
		return out, err
	}
	if out.Validate() != nil || out.InvitationID != nil && *out.InvitationID != id {
		return staffcontract.StaffInvitationReceipt{}, ErrProtocol
	}
	return out, nil
}
