package zitadel

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tesserix/onboarding-service/api/customercontract"
)

func (c *Client) identityCredentials(ctx context.Context, subject string, raw json.RawMessage, out customercontract.IdentityProjection) (customercontract.IdentityProjection, error) {
	fail := func() (customercontract.IdentityProjection, error) {
		return customercontract.IdentityProjection{}, ErrIdentityUnavailable
	}
	root, err := identityObject(raw, "user")
	if err != nil {
		return fail()
	}
	user, err := identityObject(root["user"], "human", "machine")
	if err != nil {
		return fail()
	}
	if _, machine := user["machine"]; machine {
		return fail()
	}
	human, err := identityObject(user["human"], "passwordChanged")
	if err != nil {
		return fail()
	}
	credentials := &customercontract.IdentityCredentialState{}
	if value, present := human["passwordChanged"]; present {
		var timestamp string
		if json.Unmarshal(value, &timestamp) != nil || timestamp == "" {
			return fail()
		}
		changed, err := time.Parse(time.RFC3339Nano, timestamp)
		if err != nil || !changed.After(time.Unix(0, 0)) {
			return fail()
		}
		credentials.PasswordConfigured = true
		credentials.PasswordChangedAt = changed.UTC().Format(time.RFC3339Nano)
	} else {
		// Missing change evidence is safe only when native methods prove no password exists.
		// The status read preserves the existing accepted-legacy-subject semantics.
		configured, err := c.passwordConfigured(ctx, "", subject)
		if err != nil || configured {
			return fail()
		}
	}
	out.Credentials = credentials
	if out.Validate() != nil {
		return fail()
	}
	return out, nil
}
