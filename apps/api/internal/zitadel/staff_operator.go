package zitadel

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"time"
)

// ErrStaffOperatorDenied means current source authority does not permit recovery.
var ErrStaffOperatorDenied = errors.New("staff recovery operator denied")

// StaffRecoveryOperatorProof contains current authority, without source profile data.
type StaffRecoveryOperatorProof struct {
	Subject, OrganizationID, ProjectID, GrantID string
	Write                                       bool
	VerifiedAt                                  time.Time
}

func (StaffRecoveryOperatorProof) String() string { return "[redacted staff recovery authority]" }

type operatorObject struct {
	id, kind string
	types    []string
}

var operatorOrgEvents = []string{"org.added", "org.deactivated", "org.reactivated", "org.removed"}
var operatorProjectEvents = []string{"project.added", "project.deactivated", "project.reactivated", "project.removed", "project.role.added", "project.role.changed", "project.role.removed"}
var operatorGrantEvents = []string{"user.grant.added", "user.grant.changed", "user.grant.cascade.changed", "user.grant.removed", "user.grant.cascade.removed", "user.grant.deactivated", "user.grant.reactivated"}

// VerifyStaffRecoveryOperator checks authoritative events as well as source projections.
func (c *Client) VerifyStaffRecoveryOperator(ctx context.Context, subject, project string, write bool) (StaffRecoveryOperatorProof, error) {
	empty := StaffRecoveryOperatorProof{}
	if !identitySourceID.MatchString(subject) || !identitySourceID.MatchString(project) || !identitySourceID.MatchString(c.adminOrg) {
		return empty, ErrStaffOperatorDenied
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return empty, ErrIdentityUnavailable
	}
	client := *c.http
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	before, err := c.readIdentityUser(ctx, &client, token, c.adminOrg, subject)
	if err != nil {
		return empty, err
	}
	if before.state != "active" || !before.verified {
		return empty, ErrStaffOperatorDenied
	}
	grant, err := c.readOperatorGrant(ctx, &client, token, subject, project)
	if err != nil {
		return empty, err
	}
	objects := []operatorObject{
		{subject, "user", staffIdentityEventTypes},
		{grant, "usergrant", operatorGrantEvents},
		{project, "project", operatorProjectEvents},
		{c.adminOrg, "org", operatorOrgEvents},
	}
	heads := make([]string, len(objects))
	var upper uint64
	for i, object := range objects {
		events, err := c.operatorEvents(ctx, &client, token, object, 0, 1)
		if err != nil || len(events) != 1 {
			return empty, ErrIdentityUnavailable
		}
		heads[i] = events[0].Sequence
		n, _ := strconv.ParseUint(heads[i], 10, 64)
		upper = max(upper, n)
	}
	if upper == ^uint64(0) {
		return empty, ErrIdentityUnavailable
	}
	for _, object := range objects[1:] {
		events, err := c.operatorEvents(ctx, &client, token, object, upper+1, 64)
		if err != nil {
			return empty, err
		}
		if err := checkOperatorEvents(object.kind, events, subject, project, write); err != nil {
			return empty, err
		}
	}
	if err := c.checkStaffIdentityCurrent(ctx, &client, token, StaffIdentityInput{OrganizationID: c.adminOrg, UserID: subject, Email: before.email}, before); err != nil {
		return empty, err
	}
	after, err := c.readIdentityUser(ctx, &client, token, c.adminOrg, subject)
	if err != nil || before != after || after.created.After(time.Now().Add(StaffIdentityClockSkew)) {
		return empty, ErrIdentityUnavailable
	}
	for i, object := range objects {
		events, err := c.operatorEvents(ctx, &client, token, object, 0, 1)
		if err != nil || len(events) != 1 || events[0].Sequence != heads[i] {
			return empty, ErrIdentityUnavailable
		}
	}
	if ctx.Err() != nil {
		return empty, ErrIdentityUnavailable
	}
	return StaffRecoveryOperatorProof{Subject: subject, OrganizationID: c.adminOrg, ProjectID: project, GrantID: grant, Write: write, VerifiedAt: time.Now().UTC()}, nil
}

func (c *Client) readOperatorGrant(ctx context.Context, client *http.Client, token, subject, project string) (string, error) {
	var body *struct {
		Result []struct {
			ID, UserID, ProjectID, ProjectGrantID string
			Details                               identityDetails
		}
	}
	err := c.readIdentity(ctx, client, token, http.MethodPost, "/management/v1/users/grants/_search", c.adminOrg,
		map[string]any{"query": map[string]string{"limit": "2"}, "queries": []map[string]any{
			{"userIdQuery": map[string]string{"userId": subject}},
			{"projectIdQuery": map[string]string{"projectId": project}},
		}}, &body)
	if err != nil || body == nil || len(body.Result) > 1 {
		return "", ErrIdentityUnavailable
	}
	if len(body.Result) == 0 {
		return "", ErrStaffOperatorDenied
	}
	g := body.Result[0]
	if !identitySourceID.MatchString(g.ID) || g.UserID != subject || g.ProjectID != project || g.ProjectGrantID != "" || g.Details.ResourceOwner != c.adminOrg {
		return "", ErrStaffOperatorDenied
	}
	return g.ID, nil
}

func (c *Client) operatorEvents(ctx context.Context, client *http.Client, token string, object operatorObject, upper uint64, limit int) ([]staffIdentityEvent, error) {
	var body *struct{ Events []staffIdentityEvent }
	err := c.readIdentity(ctx, client, token, http.MethodPost, "/admin/v1/events/_search", c.adminOrg,
		map[string]any{"aggregateId": object.id, "aggregateTypes": []string{object.kind}, "resourceOwner": c.adminOrg,
			"eventTypes": object.types, "sequence": strconv.FormatUint(upper, 10), "asc": false, "limit": limit}, &body)
	if err != nil || body == nil || len(body.Events) > limit || (limit > 1 && len(body.Events) == limit) {
		return nil, ErrIdentityUnavailable
	}
	previous := upper
	for _, event := range body.Events {
		n, err := strconv.ParseUint(event.Sequence, 10, 64)
		if err != nil || !validIdentityRevision(event.Sequence) || (previous != 0 && n >= previous) || event.Aggregate.ID != object.id || event.Aggregate.ResourceOwner != c.adminOrg || event.Aggregate.Type.Type != object.kind || !slices.Contains(object.types, event.Type.Type) {
			return nil, ErrIdentityUnavailable
		}
		previous = n
	}
	return body.Events, nil
}

func checkOperatorEvents(kind string, events []staffIdentityEvent, subject, project string, write bool) error {
	if len(events) == 0 {
		return ErrIdentityUnavailable
	}
	if kind == "org" {
		if events[0].Type.Type != "org.added" && events[0].Type.Type != "org.reactivated" {
			return ErrStaffOperatorDenied
		}
		return nil
	}
	if kind == "project" {
		return checkOperatorProject(events, write)
	}
	return checkOperatorGrant(events, subject, project, write)
}

func checkOperatorProject(events []staffIdentityEvent, write bool) error {
	state, platform, rotate := "", "", ""
	for _, event := range events {
		switch event.Type.Type {
		case "project.role.added", "project.role.changed", "project.role.removed":
			if event.Payload.Key == "" {
				return ErrIdentityUnavailable
			}
			if event.Payload.Key == "platform" && platform == "" {
				platform = event.Type.Type
			}
			if event.Payload.Key == "rotate-credentials" && rotate == "" {
				rotate = event.Type.Type
			}
		default:
			if state == "" {
				state = event.Type.Type
			}
		}
	}
	if state == "" || platform == "" || (write && rotate == "") {
		return ErrIdentityUnavailable
	}
	if (state != "project.added" && state != "project.reactivated") || platform == "project.role.removed" || (write && rotate == "project.role.removed") {
		return ErrStaffOperatorDenied
	}
	return nil
}

func checkOperatorGrant(events []staffIdentityEvent, subject, project string, write bool) error {
	state, bound := "", false
	var roles []string
	rolesSeen := false
	for _, event := range events {
		switch event.Type.Type {
		case "user.grant.added":
			if bound || event.Payload.UserID != subject || event.Payload.ProjectID != project || event.Payload.GrantID != "" {
				return ErrIdentityUnavailable
			}
			bound = true
			if state == "" {
				state = "active"
			}
			if !rolesSeen {
				roles, rolesSeen = event.Payload.RoleKeys, true
			}
		case "user.grant.changed", "user.grant.cascade.changed":
			if (event.Type.Type == "user.grant.changed" && event.Payload.UserID != subject) || (event.Payload.UserID != "" && event.Payload.UserID != subject) {
				return ErrIdentityUnavailable
			}
			if !rolesSeen {
				roles, rolesSeen = event.Payload.RoleKeys, true
			}
		case "user.grant.reactivated":
			if state == "" {
				state = "active"
			}
		default:
			if state == "" {
				state = "inactive"
			}
		}
	}
	if !bound || !rolesSeen {
		return ErrIdentityUnavailable
	}
	if state != "active" || !slices.Contains(roles, "platform") || (write && !slices.Contains(roles, "rotate-credentials")) {
		return ErrStaffOperatorDenied
	}
	return nil
}
