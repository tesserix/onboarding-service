package zitadel

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"
)

// StaffIdentityClockSkew bounds provider creation timestamps against the owner clock.
const StaffIdentityClockSkew = time.Minute

// StaffIdentityObservation contains minimized current proof, not membership authority.
type StaffIdentityObservation struct {
	UserID           string
	OrganizationID   string
	State            string
	EmailVerified    bool
	CreatedAt        time.Time
	ObservedAt       time.Time
	UserRevision     string
	MetadataRevision string
}

type staffUserProof struct {
	id, org, email, state, revision string
	verified                        bool
	created                         time.Time
}

// ObserveStaffIdentity reads exact user/correlation facts without creating or verifying accounts.
func (c *Client) ObserveStaffIdentity(ctx context.Context, in StaffIdentityInput, created time.Time) (StaffIdentityObservation, error) {
	empty := StaffIdentityObservation{}
	if err := in.Validate(); err != nil {
		return empty, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return empty, ErrIdentityUnavailable
	}
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	before, err := c.readStaffUser(ctx, &client, token, in)
	if err != nil {
		return empty, err
	}
	if !created.IsZero() && !before.created.Equal(created) {
		return empty, ErrIdentityUnavailable
	}
	var metadata struct {
		Metadata struct {
			Key, Value string
			Details    identityDetails
		}
	}
	if err := c.readIdentity(ctx, &client, token, http.MethodGet,
		"/management/v1/users/"+in.UserID+"/metadata/onboarding_staff_command", in.OrganizationID, nil, &metadata); err != nil {
		return empty, ErrIdentityUnavailable
	}
	binding, err := base64.StdEncoding.DecodeString(metadata.Metadata.Value)
	if err != nil || metadata.Metadata.Key != "onboarding_staff_command" || string(binding) != in.BindingNonce || metadata.Metadata.Details.ResourceOwner != in.OrganizationID || !validIdentityRevision(metadata.Metadata.Details.Sequence) {
		return empty, ErrIdentityUnavailable
	}
	after, err := c.readStaffUser(ctx, &client, token, in)
	if err != nil {
		return empty, err
	}
	if before != after || ctx.Err() != nil || after.created.After(time.Now().Add(StaffIdentityClockSkew)) {
		return empty, ErrIdentityUnavailable
	}
	if err := c.checkStaffIdentityCurrent(ctx, &client, token, in, after); err != nil {
		return empty, err
	}
	if err := c.checkStaffIdentityEvents(ctx, &client, token, in, after, metadata.Metadata.Details.Sequence); err != nil {
		return empty, err
	}
	return StaffIdentityObservation{UserID: after.id, OrganizationID: after.org,
		State: after.state, EmailVerified: after.verified, CreatedAt: after.created,
		ObservedAt: time.Now().UTC(), UserRevision: after.revision,
		MetadataRevision: metadata.Metadata.Details.Sequence}, nil
}

func (c *Client) readStaffUser(ctx context.Context, client *http.Client, token string, in StaffIdentityInput) (staffUserProof, error) {
	proof, err := c.readIdentityUser(ctx, client, token, in.OrganizationID, in.UserID)
	if err != nil {
		return staffUserProof{}, err
	}
	if proof.email != in.Email {
		return staffUserProof{}, ErrIdentityUnavailable
	}
	return proof, nil
}

func (c *Client) readIdentityUser(ctx context.Context, client *http.Client, token, org, subject string) (staffUserProof, error) {
	empty := staffUserProof{}
	var body struct {
		User struct {
			UserID, State string
			Details       struct {
				Sequence, ResourceOwner, CreationDate string
			}
			Human *struct {
				UserID, State string
				Email         *struct {
					Email      string
					IsVerified bool
				}
			}
			Machine json.RawMessage
		}
	}
	if err := c.readIdentity(ctx, client, token, http.MethodGet, "/v2/users/"+subject, org, nil, &body); err != nil {
		return empty, err
	}
	u := body.User
	if u.UserID != subject || u.Details.ResourceOwner != org || !validIdentityRevision(u.Details.Sequence) {
		return empty, ErrIdentityUnavailable
	}
	if u.State == "USER_STATE_DELETED" {
		return empty, ErrIdentityMissing
	}
	state := ""
	switch u.State {
	case "USER_STATE_ACTIVE":
		state = "active"
	case "USER_STATE_INACTIVE":
		state = "inactive"
	case "USER_STATE_LOCKED":
		state = "locked"
	case "USER_STATE_INITIAL":
		state = "initial"
	default:
		return empty, ErrIdentityUnavailable
	}
	if u.Human == nil || u.Human.Email == nil || len(u.Machine) != 0 || u.Human.Email.Email == "" || (u.Human.UserID != "" && u.Human.UserID != subject) || (u.Human.State != "" && u.Human.State != u.State) {
		return empty, ErrIdentityUnavailable
	}
	created, err := time.Parse(time.RFC3339Nano, u.Details.CreationDate)
	if err != nil || created.IsZero() {
		return empty, ErrIdentityUnavailable
	}
	return staffUserProof{id: u.UserID, org: u.Details.ResourceOwner, email: u.Human.Email.Email,
		state: state, revision: u.Details.Sequence, verified: u.Human.Email.IsVerified,
		created: created.UTC()}, nil
}
