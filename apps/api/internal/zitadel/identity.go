package zitadel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrIdentityUnavailable = errors.New("identity source unavailable")
	ErrIdentityMissing     = errors.New("identity source not found")
	identitySourceID       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
)

const identityReadLimit = 64 << 10

type IdentityRevisions struct {
	Organization string `json:"organization"`
	Metadata     string `json:"metadata"`
	Project      string `json:"project"`
	Grant        string `json:"grant"`
}

type OrganizationIdentity struct {
	SchemaVersion   int               `json:"schema_version"`
	TenantID        string            `json:"tenant_id"`
	OrganizationID  string            `json:"organization_id"`
	ProjectID       string            `json:"project_id"`
	GrantID         string            `json:"grant_id"`
	State           string            `json:"state"`
	VerifiedAt      time.Time         `json:"verified_at"`
	SourceRevisions IdentityRevisions `json:"source_revisions"`
}

type identityDetails struct {
	Sequence      string `json:"sequence"`
	ResourceOwner string `json:"resourceOwner"`
}

type identitySourceObject struct {
	ID      string          `json:"id"`
	State   string          `json:"state"`
	Details identityDetails `json:"details"`
}

type identitySourceGrant struct {
	GrantID        string          `json:"grantId"`
	GrantedOrgID   string          `json:"grantedOrgId"`
	ProjectID      string          `json:"projectId"`
	ProjectOwnerID string          `json:"projectOwnerId"`
	State          string          `json:"state"`
	Details        identityDetails `json:"details"`
}

func (c *Client) VerifyOrganization(ctx context.Context, orgID, tenantID, projectID string) (OrganizationIdentity, error) {
	empty := OrganizationIdentity{}
	tenant, err := uuid.Parse(tenantID)
	if err != nil || tenant == uuid.Nil || !identitySourceID.MatchString(orgID) || !identitySourceID.MatchString(projectID) || !identitySourceID.MatchString(c.adminOrg) {
		return empty, ErrIdentityUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	token, err := c.tokens.Token(ctx)
	if err != nil || token == "" {
		return empty, ErrIdentityUnavailable
	}
	client := *c.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var org struct {
		Org identitySourceObject `json:"org"`
	}
	var project struct {
		Project identitySourceObject `json:"project"`
	}
	var metadata struct {
		Metadata struct {
			Key     string          `json:"key"`
			Value   string          `json:"value"`
			Details identityDetails `json:"details"`
		} `json:"metadata"`
	}
	var grants struct {
		Details struct {
			TotalResult string `json:"totalResult"`
		} `json:"details"`
		Result []identitySourceGrant `json:"result"`
	}
	query := map[string]any{
		"query": map[string]string{"limit": "2"},
		"queries": []map[string]any{
			{"projectIdQuery": map[string]string{"projectId": projectID}},
			{"grantedOrgIdQuery": map[string]string{"grantedOrgId": orgID}},
		},
	}
	calls := []struct {
		method, path, org string
		body, out         any
	}{
		{http.MethodGet, "/admin/v1/orgs/" + orgID, "", nil, &org},
		{http.MethodGet, "/management/v1/metadata/tenant_id", orgID, nil, &metadata},
		{http.MethodGet, "/management/v1/projects/" + projectID, c.adminOrg, nil, &project},
		{http.MethodPost, "/management/v1/projectgrants/_search", c.adminOrg, query, &grants},
	}
	errs := make([]error, len(calls))
	var wg sync.WaitGroup
	for i, call := range calls {
		wg.Go(func() {
			errs[i] = c.readIdentity(ctx, &client, token, call.method, call.path, call.org, call.body, call.out)
		})
	}
	wg.Wait()
	for _, e := range errs {
		if e != nil && !errors.Is(e, ErrIdentityMissing) {
			return empty, ErrIdentityUnavailable
		}
	}
	for _, e := range errs {
		if e != nil {
			return empty, ErrIdentityMissing
		}
	}
	if len(grants.Result) == 0 {
		if grants.Details.TotalResult == "0" {
			return empty, ErrIdentityMissing
		}
		return empty, ErrIdentityUnavailable
	}
	if len(grants.Result) != 1 || grants.Details.TotalResult != "1" {
		return empty, ErrIdentityUnavailable
	}
	grant := grants.Result[0]
	searchedState := grant.State
	if org.Org.ID != orgID || project.Project.ID != projectID || project.Project.Details.ResourceOwner != c.adminOrg || grant.GrantedOrgID != orgID || grant.ProjectID != projectID || grant.ProjectOwnerID != c.adminOrg || !identitySourceID.MatchString(grant.GrantID) || metadata.Metadata.Key != "tenant_id" {
		return empty, ErrIdentityUnavailable
	}
	var current struct {
		Grant identitySourceGrant `json:"projectGrant"`
	}
	// Unlike grant search, this source read awaits the grant projection (ADR0003).
	if err := c.readIdentity(ctx, &client, token, http.MethodGet, "/management/v1/projects/"+projectID+"/grants/"+grant.GrantID, c.adminOrg, nil, &current); err != nil {
		return empty, err
	}
	latest := current.Grant
	if latest.GrantID != grant.GrantID || latest.GrantedOrgID != orgID || latest.ProjectID != projectID || latest.ProjectOwnerID != c.adminOrg || latest.Details.ResourceOwner != c.adminOrg {
		return empty, ErrIdentityUnavailable
	}
	searchedSequence, searchErr := strconv.ParseUint(grant.Details.Sequence, 10, 64)
	latestSequence, latestErr := strconv.ParseUint(latest.Details.Sequence, 10, 64)
	if searchErr != nil || latestErr != nil || searchedSequence == 0 || strconv.FormatUint(searchedSequence, 10) != grant.Details.Sequence || latestSequence < searchedSequence {
		return empty, ErrIdentityUnavailable
	}
	grant = latest
	decoded, err := base64.StdEncoding.DecodeString(metadata.Metadata.Value)
	if err != nil {
		return empty, ErrIdentityUnavailable
	}
	binding, err := uuid.Parse(string(decoded))
	if err != nil || binding != tenant {
		return empty, ErrIdentityUnavailable
	}
	revisions := IdentityRevisions{Organization: org.Org.Details.Sequence, Metadata: metadata.Metadata.Details.Sequence, Project: project.Project.Details.Sequence, Grant: grant.Details.Sequence}
	for _, seq := range []string{revisions.Organization, revisions.Metadata, revisions.Project, revisions.Grant} {
		v, e := strconv.ParseUint(seq, 10, 64)
		if e != nil || v == 0 || strconv.FormatUint(v, 10) != seq {
			return empty, ErrIdentityUnavailable
		}
	}
	active := true
	for _, state := range []struct{ value, prefix string }{{org.Org.State, "ORG_STATE_"}, {project.Project.State, "PROJECT_STATE_"}, {searchedState, "PROJECT_GRANT_STATE_"}, {grant.State, "PROJECT_GRANT_STATE_"}} {
		switch state.value {
		case state.prefix + "ACTIVE":
		case state.prefix + "INACTIVE":
			active = false
		default:
			return empty, ErrIdentityUnavailable
		}
	}
	state := "inactive"
	if active {
		state = "active"
	}
	return OrganizationIdentity{SchemaVersion: 1, TenantID: tenant.String(), OrganizationID: orgID, ProjectID: projectID, GrantID: grant.GrantID, State: state, VerifiedAt: time.Now().UTC(), SourceRevisions: revisions}, nil
}

func (c *Client) readIdentity(ctx context.Context, client *http.Client, token, method, path, org string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return ErrIdentityUnavailable
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return ErrIdentityUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	if org != "" {
		req.Header.Set("x-zitadel-orgid", org)
	}
	response, err := client.Do(req)
	if err != nil {
		return ErrIdentityUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return ErrIdentityMissing
	}
	if response.StatusCode != http.StatusOK {
		return ErrIdentityUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, identityReadLimit+1))
	if err != nil || len(raw) > identityReadLimit || len(raw) == 0 {
		return ErrIdentityUnavailable
	}
	if json.Unmarshal(raw, out) != nil {
		return ErrIdentityUnavailable
	}
	return nil
}
