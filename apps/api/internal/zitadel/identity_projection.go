package zitadel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/tesserix/onboarding-service/api/customercontract"
)

func (c *Client) IdentityProjection(ctx context.Context, org, subject, purpose string) (customercontract.IdentityProjection, error) {
	base := customercontract.IdentityProjection{TenantID: strings.TrimPrefix(org, "helivanta-"), OrganizationID: org, Subject: subject, State: "inactive", Admission: "unassessed", Purpose: purpose}
	if purpose == "membership" {
		base.Admission = "ineligible"
	}
	if c == nil || c.tokens == nil || c.http == nil || base.Validate() != nil {
		return customercontract.IdentityProjection{}, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return customercontract.IdentityProjection{}, ErrIdentityUnavailable
	}
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var raw json.RawMessage
	if err = c.readIdentity(ctx, &client, token, http.MethodGet, "/v2/users/"+url.PathEscape(subject), "", nil, &raw); err != nil {
		if errors.Is(err, ErrIdentityMissing) {
			return base, nil
		}
		return customercontract.IdentityProjection{}, ErrIdentityUnavailable
	}
	return nativeIdentityProjection(raw, base)
}

func nativeIdentityProjection(raw json.RawMessage, out customercontract.IdentityProjection) (customercontract.IdentityProjection, error) {
	fail := func() (customercontract.IdentityProjection, error) {
		return customercontract.IdentityProjection{}, ErrIdentityUnavailable
	}
	root, err := identityObject(raw, "user")
	if err != nil {
		return fail()
	}
	user, err := identityObject(root["user"], "userId", "state", "human", "machine", "details")
	if err != nil {
		return fail()
	}
	var id, state string
	if json.Unmarshal(user["userId"], &id) != nil || id != out.Subject || json.Unmarshal(user["state"], &state) != nil {
		return fail()
	}
	switch state {
	case "USER_STATE_ACTIVE":
		out.State = "active"
	case "USER_STATE_INACTIVE", "USER_STATE_LOCKED", "USER_STATE_SUSPEND", "USER_STATE_DELETED", "USER_STATE_INITIAL":
		return out, nil
	default:
		return fail()
	}
	if out.Purpose == "status" {
		return out, nil
	}
	human, hasHuman := user["human"]
	_, hasMachine := user["machine"]
	if hasHuman && hasMachine {
		return fail()
	}
	if !hasHuman {
		if hasMachine {
			return out, nil
		}
		return fail()
	}
	person, err := identityObject(human, "email", "profile")
	if err != nil {
		return fail()
	}
	email, err := identityObject(person["email"], "email", "isVerified")
	if err != nil {
		return fail()
	}

	if out.Purpose == "profile" {
		var address string
		if json.Unmarshal(email["email"], &address) != nil {
			return fail()
		}
		parsed, err := mail.ParseAddress(address)
		if err != nil || parsed.Address != address {
			return fail()
		}
		if profile, present := person["profile"]; present {
			values, err := identityObject(profile, "displayName")
			if err != nil {
				return fail()
			}
			var name string
			if value, exists := values["displayName"]; exists {
				if json.Unmarshal(value, &name) != nil {
					return fail()
				}
				if name != "" {
					out.Profile = &customercontract.IdentityProfile{DisplayName: name, Email: address}
					if out.Profile.Validate() != nil {
						return fail()
					}
				}
			}
		}

		return out, nil
	}
	verified, present := email["isVerified"]
	if !present {
		return out, nil
	}
	var confirmed bool
	if string(verified) == "null" || json.Unmarshal(verified, &confirmed) != nil {
		return fail()
	}
	if !confirmed {
		return out, nil
	}
	var address string
	if json.Unmarshal(email["email"], &address) != nil {
		return fail()
	}
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Address != address || strings.TrimSpace(address) != address {
		return fail()
	}
	details, err := identityObject(user["details"], "resourceOwner")
	if err != nil {
		return fail()
	}
	var owner string
	if json.Unmarshal(details["resourceOwner"], &owner) != nil || owner == "" {
		return fail()
	}
	if owner != out.OrganizationID {
		return out, nil
	}
	out.Admission = "eligible"
	return out, nil
}

func identityObject(raw json.RawMessage, names ...string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, ErrIdentityUnavailable
	}
	fields := make(map[string]json.RawMessage)
	normalize := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "_", "")) }
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, ErrIdentityUnavailable
		}
		key, ok := token.(string)
		if !ok {
			return nil, ErrIdentityUnavailable
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, ErrIdentityUnavailable
		}
		for _, name := range names {
			if key != name && normalize(key) == normalize(name) {
				return nil, ErrIdentityUnavailable
			}
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return nil, ErrIdentityUnavailable
		}
		fields[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, ErrIdentityUnavailable
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrIdentityUnavailable
	}
	return fields, nil
}

// confirmedMissingUser recognises the GetUserByID scanUser error from the pinned
// native API. A gateway's generic404 is not evidence of a deleted identity.
func confirmedMissingUser(raw json.RawMessage) bool {
	root, err := identityObject(raw, "code", "details")
	if err != nil {
		return false
	}
	var code int
	if json.Unmarshal(root["code"], &code) != nil || code != 5 {
		return false
	}
	var details []json.RawMessage
	if json.Unmarshal(root["details"], &details) != nil || len(details) != 1 {
		return false
	}
	proof, err := identityObject(details[0], "@type", "id")
	if err != nil {
		return false
	}
	var kind, id string
	return json.Unmarshal(proof["@type"], &kind) == nil && json.Unmarshal(proof["id"], &id) == nil && kind == "type.googleapis.com/zitadel.v1.ErrorDetail" && id == "QUERY-Dfbg2"
}
