package zitadel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrStaffIdentityInvalid   = errors.New("invalid staff identity input")
	ErrStaffIdentityUncertain = errors.New("staff identity creation requires reconciliation")
)

// StaffIdentityInput contains immutable facts from the owner's persisted command.
type StaffIdentityInput struct {
	OrganizationID string
	UserID         string
	Email          string
	GivenName      string
	FamilyName     string
	BindingNonce   string
}

func (StaffIdentityInput) String() string { return "[redacted staff identity input]" }

// StaffIdentityCreated records creation only, never contact or access eligibility.
type StaffIdentityCreated struct {
	ID        string
	CreatedAt time.Time
}

// CreateStaffIdentity dispatches once; callers must persist the dispatch claim first.
func (c *Client) CreateStaffIdentity(ctx context.Context, in StaffIdentityInput) (StaffIdentityCreated, error) {
	empty := StaffIdentityCreated{}
	address, err := mail.ParseAddress(in.Email)
	if err != nil || address.Address != in.Email || len(in.Email) > 200 || !identitySourceID.MatchString(in.OrganizationID) || !identitySourceID.MatchString(in.UserID) || !identitySourceID.MatchString(in.BindingNonce) || len(in.BindingNonce) < 16 {
		return empty, ErrStaffIdentityInvalid
	}
	for _, name := range []string{in.GivenName, in.FamilyName} {
		if !utf8.ValidString(name) || strings.TrimSpace(name) != name || name == "" || len(name) > 200 || strings.ContainsFunc(name, unicode.IsControl) {
			return empty, ErrStaffIdentityInvalid
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return empty, ErrIdentityUnavailable
	}
	raw, err := json.Marshal(map[string]any{
		"organizationId": in.OrganizationID, "userId": in.UserID, "username": in.Email,
		"human": map[string]any{
			"profile": map[string]string{"givenName": in.GivenName, "familyName": in.FamilyName},
			"email":   map[string]string{"email": in.Email},
		},
		"metadata": []map[string]string{{"key": "onboarding_staff_command", "value": base64.StdEncoding.EncodeToString([]byte(in.BindingNonce))}},
	})
	if err != nil {
		return empty, ErrStaffIdentityInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v2/users/new", bytes.NewReader(raw))
	if err != nil {
		return empty, ErrIdentityUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-zitadel-orgid", in.OrganizationID)
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		return empty, ErrStaffIdentityUncertain
	}
	defer func() { _ = response.Body.Close() }()
	// Any response after dispatch may hide a committed creation; never retry it.
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated {
		return empty, ErrStaffIdentityUncertain
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, identityReadLimit+1))
	if err != nil || len(body) > identityReadLimit {
		return empty, ErrStaffIdentityUncertain
	}
	proof, err := staffCreationProof(body)
	if err != nil || proof.ID != in.UserID {
		return empty, ErrStaffIdentityUncertain
	}
	return proof, nil
}

func staffCreationProof(raw []byte) (StaffIdentityCreated, error) {
	empty := StaffIdentityCreated{}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return empty, ErrStaffIdentityUncertain
	}
	fields := map[string]string{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || (key != "id" && key != "creationDate") {
			return empty, ErrStaffIdentityUncertain
		}
		if _, duplicate := fields[key]; duplicate {
			return empty, ErrStaffIdentityUncertain
		}
		var value string
		if decoder.Decode(&value) != nil || value == "" {
			return empty, ErrStaffIdentityUncertain
		}
		fields[key] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(fields) != 2 {
		return empty, ErrStaffIdentityUncertain
	}
	if _, err := decoder.Token(); err != io.EOF {
		return empty, ErrStaffIdentityUncertain
	}
	created, err := time.Parse(time.RFC3339Nano, fields["creationDate"])
	if err != nil || created.IsZero() || !identitySourceID.MatchString(fields["id"]) {
		return empty, ErrStaffIdentityUncertain
	}
	return StaffIdentityCreated{ID: fields["id"], CreatedAt: created}, nil
}
