// Package zitadel wraps the management API. It is the only place in the estate
// that holds the iam-admin credential; every /management/v1 call is org-scoped
// by the x-zitadel-orgid header, and omitting it operates on the wrong
// organization silently.
package zitadel

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	base     string
	tokens   TokenSource
	adminOrg string // TESSERIX — the org that owns every product project
	http     *http.Client
	log      *slog.Logger
}

func New(baseURL string, tokens TokenSource, adminOrgID string, log *slog.Logger) *Client {
	if log == nil {
		log = slog.Default()
	}
	return &Client{
		base:     strings.TrimSuffix(baseURL, "/"),
		tokens:   tokens,
		adminOrg: adminOrgID,
		http:     &http.Client{Timeout: 20 * time.Second},
		log:      log,
	}
}

type APIError struct {
	Status int
	Method string
	Path   string
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("zitadel: %s %s: %d: %s", e.Method, e.Path, e.Status, e.Body)
}

func (c *Client) do(ctx context.Context, method, path, orgID string, in, out any) error {
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("zitadel: encode %s: %w", path, err)
		}
		body = bytes.NewReader(encoded)
	}
	token, err := c.tokens.Token(ctx)
	if err != nil {
		return fmt.Errorf("zitadel: %s %s: %w", method, path, err)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return fmt.Errorf("zitadel: build %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	if orgID != "" {
		req.Header.Set("x-zitadel-orgid", orgID)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("zitadel: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return &APIError{Status: resp.StatusCode, Method: method, Path: path, Body: string(raw)}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("zitadel: decode %s: %w", path, err)
		}
	}
	return nil
}

// ResolveOrgByName turns the platform org's name into its id at boot. Anything
// but exactly one match is an error: a wrong id here would scope every
// management call to the wrong tenant, and silently.
func (c *Client) ResolveOrgByName(ctx context.Context, name string) (string, error) {
	var out struct {
		Result []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"result"`
	}
	query := map[string]any{
		"queries": []map[string]any{
			{"nameQuery": map[string]any{"name": name, "method": "TEXT_QUERY_METHOD_EQUALS"}},
		},
	}
	if err := c.do(ctx, http.MethodPost, "/admin/v1/orgs/_search", "", query, &out); err != nil {
		return "", err
	}
	// The query asks for an exact match, so more than one result means it did
	// not behave as asked — picking the first would be a guess.
	if len(out.Result) != 1 || out.Result[0].Name != name {
		return "", fmt.Errorf("zitadel: search for organization %q returned %d results, want exactly one exact match",
			name, len(out.Result))
	}
	return out.Result[0].ID, nil
}

func (c *Client) CreateOrg(ctx context.Context, name string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/management/v1/orgs", "", map[string]any{"name": name}, &out); err != nil {
		return "", err
	}
	return out.ID, nil
}

// RemoveOrg is only ever the compensation for a create that failed part-way.
// Nothing routed from the console deletes an organization.
func (c *Client) RemoveOrg(ctx context.Context, orgID string) error {
	return c.do(ctx, http.MethodDelete, "/management/v1/orgs/me", orgID, nil, nil)
}

func (c *Client) DeactivateOrg(ctx context.Context, orgID string) error {
	return c.do(ctx, http.MethodPost, "/management/v1/orgs/me/_deactivate", orgID, map[string]any{}, nil)
}

func (c *Client) ReactivateOrg(ctx context.Context, orgID string) error {
	return c.do(ctx, http.MethodPost, "/management/v1/orgs/me/_reactivate", orgID, map[string]any{}, nil)
}

// SetOrgMetadata is how tenant_id reaches the token: org metadata is projected
// into the claim by the Action, and the product reads it there.
//
// The path is /metadata/{key}, not /orgs/me/metadata/{key} as the neighbouring
// org calls would suggest — here the org comes from the header alone.
func (c *Client) SetOrgMetadata(ctx context.Context, orgID, key, value string) error {
	return c.do(ctx, http.MethodPost, "/management/v1/metadata/"+key, orgID,
		map[string]any{"value": base64.StdEncoding.EncodeToString([]byte(value))}, nil)
}

func (c *Client) AddOrgMember(ctx context.Context, orgID, userID, role string) error {
	return c.do(ctx, http.MethodPost, "/management/v1/orgs/me/members", orgID,
		map[string]any{"userId": userID, "roles": []string{role}}, nil)
}

func (c *Client) RemoveOrgMember(ctx context.Context, orgID, userID string) error {
	return c.do(ctx, http.MethodDelete, "/management/v1/orgs/me/members/"+userID, orgID, nil, nil)
}

type OrgMember struct {
	UserID      string   `json:"userId"`
	Email       string   `json:"preferredLoginName"`
	DisplayName string   `json:"displayName"`
	Roles       []string `json:"roles"`
}

func (c *Client) ListOrgMembers(ctx context.Context, orgID string) ([]OrgMember, error) {
	var out struct {
		Result []OrgMember `json:"result"`
	}
	err := c.do(ctx, http.MethodPost, "/management/v1/orgs/me/members/_search", orgID,
		map[string]any{"query": map[string]any{"limit": 500}}, &out)
	return out.Result, err
}

func (c *Client) CreateHumanUser(ctx context.Context, orgID, email, firstName, lastName string) (string, error) {
	var out struct {
		UserID string `json:"userId"`
	}
	in := map[string]any{
		"userName": email,
		"profile":  map[string]any{"firstName": orDefault(firstName, email), "lastName": orDefault(lastName, "-")},
		"email":    map[string]any{"email": email, "isEmailVerified": false},
	}
	if err := c.do(ctx, http.MethodPost, "/management/v1/users/human/_import", orgID, in, &out); err != nil {
		return "", err
	}
	return out.UserID, nil
}

// GrantProject is issued by the org that OWNS the project, not by the org
// receiving it. Scoping this to the receiving org is a silent no-op.
func (c *Client) GrantProject(ctx context.Context, projectID, grantedOrgID string, roles []string) error {
	_, err := c.CreateProjectGrant(ctx, projectID, grantedOrgID, roles)
	return err
}

// CreateProjectGrant returns the grant id needed to assign users of the
// receiving organization roles in a project owned by the platform org.
func (c *Client) CreateProjectGrant(ctx context.Context, projectID, grantedOrgID string, roles []string) (string, error) {
	if roles == nil {
		roles = []string{}
	}
	var out struct {
		GrantID string `json:"grantId"`
	}
	err := c.do(ctx, http.MethodPost, "/management/v1/projects/"+projectID+"/grants", c.adminOrg,
		map[string]any{"grantedOrgId": grantedOrgID, "roleKeys": roles}, &out)
	return out.GrantID, err
}

func (c *Client) AddOrgDomain(ctx context.Context, orgID, domain string) error {
	return c.do(ctx, http.MethodPost, "/management/v1/orgs/me/domains", orgID,
		map[string]any{"domain": domain}, nil)
}

func (c *Client) RemoveOrgDomain(ctx context.Context, orgID, domain string) error {
	return c.do(ctx, http.MethodDelete, "/management/v1/orgs/me/domains/"+domain, orgID, nil, nil)
}

type Challenge struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (c *Client) GenerateDomainChallenge(ctx context.Context, orgID, domain string) (Challenge, error) {
	var out struct {
		Token string `json:"token"`
	}
	// 2 is DNS validation; the HTTP method would require the customer to serve
	// a file, which is a worse ask than a TXT record for an enterprise IT team.
	err := c.do(ctx, http.MethodPost,
		"/management/v1/orgs/me/domains/"+domain+"/validation/_generate", orgID,
		map[string]any{"type": 2}, &out)
	if err != nil {
		return Challenge{}, err
	}
	return Challenge{Type: "TXT", Name: "_zitadel-challenge." + domain, Value: out.Token}, nil
}

func (c *Client) ValidateDomain(ctx context.Context, orgID, domain string) error {
	return c.do(ctx, http.MethodPost,
		"/management/v1/orgs/me/domains/"+domain+"/validation/_validate", orgID, map[string]any{}, nil)
}

// IDPSpec carries a customer's secret, so it must never be rendered into a log
// line or an error string — hence String().
type IDPSpec struct {
	Type         string
	Name         string
	Issuer       string
	ClientID     string
	ClientSecret string
	TenantID     string // Entra
	MetadataXML  string // SAML
	Scopes       []string
	AutoRegister bool
}

func (s IDPSpec) String() string {
	return fmt.Sprintf("IDPSpec{type:%s name:%s clientId:%s secret:[redacted]}", s.Type, s.Name, s.ClientID)
}

// endpoint per type. Entra is deliberately NOT the generic OIDC endpoint:
// tenant-ID routing and non-standard claims mean email and groups arrive in the
// wrong shape otherwise.
var idpEndpoints = map[string]string{
	"oidc":   "/management/v1/idps/oidc",
	"okta":   "/management/v1/idps/oidc",
	"entra":  "/management/v1/idps/azure",
	"google": "/management/v1/idps/google",
	"github": "/management/v1/idps/github",
	"saml":   "/management/v1/idps/saml",
	"ldap":   "/management/v1/idps/ldap",
}

// CreateIDP creates the provider AND binds it to the org's login policy. An
// unbound IdP never appears at the login screen, which is the single most
// common onboarding failure, so the unbound state is never exposed: if the bind
// fails the provider is removed again.
func (c *Client) CreateIDP(ctx context.Context, orgID string, spec IDPSpec) (string, error) {
	endpoint, known := idpEndpoints[spec.Type]
	if !known {
		return "", fmt.Errorf("zitadel: unsupported identity provider type %q", spec.Type)
	}

	var out struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, endpoint, orgID, idpPayload(spec), &out); err != nil {
		return "", err
	}

	if err := c.bindIDP(ctx, orgID, out.ID); err != nil {
		if rollback := c.DeleteIDP(ctx, orgID, out.ID); rollback != nil {
			c.log.Error("idp bind failed and rollback failed; provider is orphaned",
				"org", orgID, "idp", out.ID, "error", rollback)
		}
		return "", fmt.Errorf("bind idp to login policy: %w", err)
	}
	return out.ID, nil
}

func (c *Client) bindIDP(ctx context.Context, orgID, idpID string) error {
	return c.do(ctx, http.MethodPost, "/management/v1/policies/login/idps", orgID,
		map[string]any{"idpId": idpID, "ownerType": "IDP_OWNER_TYPE_ORG"}, nil)
}

func (c *Client) DeleteIDP(ctx context.Context, orgID, idpID string) error {
	return c.do(ctx, http.MethodDelete, "/management/v1/idps/"+idpID, orgID, nil, nil)
}

func idpPayload(spec IDPSpec) map[string]any {
	// autoRegister off by default is deliberate: on, anyone who can
	// authenticate at the customer's IdP is provisioned into their org.
	payload := map[string]any{
		"name": spec.Name,
		"providerOptions": map[string]any{
			"isAutoCreation":    spec.AutoRegister,
			"isLinkingAllowed":  true,
			"isCreationAllowed": true,
			"isAutoUpdate":      true,
		},
	}
	switch spec.Type {
	case "saml":
		payload["metadataXml"] = base64.StdEncoding.EncodeToString([]byte(spec.MetadataXML))
	case "entra":
		payload["clientId"] = spec.ClientID
		payload["clientSecret"] = spec.ClientSecret
		payload["tenant"] = map[string]any{"tenantId": spec.TenantID}
	default:
		payload["clientId"] = spec.ClientID
		payload["clientSecret"] = spec.ClientSecret
		if spec.Issuer != "" {
			payload["issuer"] = spec.Issuer
		}
		if len(spec.Scopes) > 0 {
			payload["scopes"] = spec.Scopes
		}
	}
	return payload
}

func (c *Client) CreateProject(ctx context.Context, name string, roles []string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	// Created in TESSERIX. A project in the reserved ZITADEL org trips
	// zitadel-bootstrap's reserved-org assertion.
	err := c.do(ctx, http.MethodPost, "/management/v1/projects", c.adminOrg, map[string]any{
		"name":                 name,
		"projectRoleAssertion": true,
		"projectRoleCheck":     true,
		"hasProjectCheck":      true,
	}, &out)
	if err != nil {
		return "", err
	}
	for _, role := range roles {
		if err := c.do(ctx, http.MethodPost, "/management/v1/projects/"+out.ID+"/roles", c.adminOrg,
			map[string]any{"roleKey": role, "displayName": role}, nil); err != nil {
			return "", fmt.Errorf("add role %s: %w", role, err)
		}
	}
	return out.ID, nil
}

type OIDCApp struct {
	AppID        string `json:"appId"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
}

func (c *Client) CreateOIDCApp(ctx context.Context, projectID, name string, redirectURIs, postLogoutURIs []string) (OIDCApp, error) {
	var out OIDCApp
	err := c.do(ctx, http.MethodPost, "/management/v1/projects/"+projectID+"/apps/oidc", c.adminOrg, map[string]any{
		"name":                     name,
		"redirectUris":             redirectURIs,
		"postLogoutRedirectUris":   postLogoutURIs,
		"responseTypes":            []string{"OIDC_RESPONSE_TYPE_CODE"},
		"grantTypes":               []string{"OIDC_GRANT_TYPE_AUTHORIZATION_CODE", "OIDC_GRANT_TYPE_REFRESH_TOKEN"},
		"appType":                  "OIDC_APP_TYPE_WEB",
		"authMethodType":           "OIDC_AUTH_METHOD_TYPE_NONE",
		"devMode":                  false,
		"accessTokenType":          "OIDC_TOKEN_TYPE_JWT",
		"accessTokenRoleAssertion": true,
		"idTokenRoleAssertion":     true,
	}, &out)
	return out, err
}

func (c *Client) CreateAPIApp(ctx context.Context, projectID, name string) (string, error) {
	var out struct {
		AppID string `json:"appId"`
	}
	err := c.do(ctx, http.MethodPost, "/management/v1/projects/"+projectID+"/apps/api", c.adminOrg,
		map[string]any{"name": name, "authMethodType": "API_AUTH_METHOD_TYPE_PRIVATE_KEY_JWT"}, &out)
	return out.AppID, err
}

type MachineKey struct {
	UserID string `json:"userId"`
	KeyID  string `json:"keyId"`
	// KeyDetails is the private key. It is returned once, written straight to
	// GCP Secret Manager, and never stored or logged here.
	KeyDetails []byte `json:"keyDetails"`
}

func (c *Client) CreateMachineUser(ctx context.Context, username, name string) (string, error) {
	return c.CreateMachineUserInOrg(ctx, c.adminOrg, username, name)
}

// CreateMachineUserInOrg keeps the credential's resource owner equal to the
// customer organization, which is the tenant claim Registry authorizes.
func (c *Client) CreateMachineUserInOrg(ctx context.Context, orgID, username, name string) (string, error) {
	var out struct {
		UserID string `json:"userId"`
	}
	err := c.do(ctx, http.MethodPost, "/management/v1/users/machine", orgID, map[string]any{
		"userName":        username,
		"name":            name,
		"accessTokenType": "ACCESS_TOKEN_TYPE_JWT",
	}, &out)
	return out.UserID, err
}

func (c *Client) AddMachineSecret(ctx context.Context, orgID, userID string) (string, error) {
	var out struct {
		ClientSecret string `json:"clientSecret"`
	}
	err := c.do(ctx, http.MethodPost, "/v2/users/"+userID+"/secret", orgID, map[string]any{}, &out)
	return out.ClientSecret, err
}

func (c *Client) RemoveMachineSecret(ctx context.Context, orgID, userID string) error {
	return c.do(ctx, http.MethodDelete, "/v2/users/"+userID+"/secret", orgID, nil, nil)
}

func (c *Client) SetUserMetadata(ctx context.Context, orgID, userID string, values map[string][]byte) error {
	entries := make([]map[string]string, 0, len(values))
	for key, value := range values {
		entries = append(entries, map[string]string{
			"key": key, "value": base64.StdEncoding.EncodeToString(value),
		})
	}
	return c.do(ctx, http.MethodPost, "/v2/users/"+userID+"/metadata", orgID,
		map[string]any{"metadata": entries}, nil)
}

func (c *Client) GrantUserProjectRoles(
	ctx context.Context,
	orgID, userID, projectID, projectGrantID string,
	roles []string,
) (string, error) {
	var out struct {
		UserGrantID string `json:"userGrantId"`
	}
	err := c.do(ctx, http.MethodPost, "/management/v1/users/"+userID+"/grants", orgID, map[string]any{
		"projectId": projectID, "projectGrantId": projectGrantID, "roleKeys": roles,
	}, &out)
	return out.UserGrantID, err
}

func (c *Client) DeactivateUser(ctx context.Context, orgID, userID string) error {
	return c.do(ctx, http.MethodPost, "/v2/users/"+userID+"/deactivate", orgID, map[string]any{}, nil)
}

func (c *Client) ReactivateUser(ctx context.Context, orgID, userID string) error {
	return c.do(ctx, http.MethodPost, "/v2/users/"+userID+"/reactivate", orgID, map[string]any{}, nil)
}

func (c *Client) CreateMachineKey(ctx context.Context, userID string) (MachineKey, error) {
	var out MachineKey
	err := c.do(ctx, http.MethodPost, "/management/v1/users/"+userID+"/keys", c.adminOrg,
		map[string]any{"type": "KEY_TYPE_JSON"}, &out)
	out.UserID = userID
	return out, err
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
