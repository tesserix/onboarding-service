package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
	"github.com/tesserix/onboarding-service/api/internal/zitadel"
)

func (d Deps) registerRoutes(g *gin.RouterGroup) {
	g.GET("/products", d.listProducts)
	g.POST("/products", d.registerProduct)
	g.GET("/products/:key", d.getProduct)
	g.POST("/products/:key/manifests-acknowledged", d.acknowledgeManifests)

	g.GET("/orgs", d.listOrgs)
	g.POST("/orgs", d.createOrg)
	g.GET("/orgs/:id", d.getOrg)
	g.GET("/orgs/:id/identity", d.getOrgIdentity)
	g.POST("/orgs/:id/deactivate", d.setOrgState("inactive"))
	g.POST("/orgs/:id/reactivate", d.setOrgState("active"))

	g.GET("/orgs/:id/grants", d.listGrants)
	g.POST("/orgs/:id/grants", d.addGrant)
	g.DELETE("/orgs/:id/grants/:productKey", d.revokeGrant)

	g.GET("/orgs/:id/domains", d.listDomains)
	g.POST("/orgs/:id/domains", d.addDomain)
	g.POST("/orgs/:id/domains/:domain/verify", d.verifyDomain)
	g.DELETE("/orgs/:id/domains/:domain", d.removeDomain)

	g.GET("/orgs/:id/idps", d.listIDPs)
	g.POST("/orgs/:id/idps", d.createIDP)
	g.DELETE("/orgs/:id/idps/:idpId", d.deleteIDP)

	g.GET("/orgs/:id/members", d.listMembers)
	g.POST("/orgs/:id/members", d.addMember)
	g.DELETE("/orgs/:id/members/:userId", d.removeMember)

	g.POST("/discovery/resolve", d.resolve)

	g.GET("/audit", d.listAudit)
	g.GET("/orgs/:id/audit", d.listOrgAudit)
	g.GET("/me", d.me)

	g.POST("/registry/tenants", d.onboardRegistryTenant)
	g.GET("/registry/credentials", d.listRegistryCredentials)
	g.POST("/registry/credentials", d.createRegistryCredential)
	g.POST("/registry/credentials/:credentialId/rotate", d.rotateRegistryCredential)
	g.DELETE("/registry/credentials/:credentialId", d.revokeRegistryCredential)
}

// orgID parses the path parameter. A malformed id is a 400 and never reaches
// the store, where it would be an error indistinguishable from a lookup failure.
func orgID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		abort(c, http.StatusBadRequest, "organization id must be a uuid")
		return uuid.UUID{}, false
	}
	return id, true
}

func (d Deps) me(c *gin.Context) {
	p := principalOf(c)
	c.JSON(http.StatusOK, gin.H{
		"kind": p.Kind, "subject": p.Subject, "productKey": p.ProductKey,
		"orgId": p.OrgID, "roles": p.Roles,
	})
}

// --- products ------------------------------------------------------------

func (d Deps) listProducts(c *gin.Context) {
	products, err := d.Service.Products(c.Request.Context(), principalOf(c))
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"products": products})
}

func (d Deps) getProduct(c *gin.Context) {
	product, err := d.Service.Product(c.Request.Context(), principalOf(c), c.Param("key"))
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, product)
}

// registerProductRequest makes the two CLAUDE.md questions mandatory fields:
// a product cannot be onboarded without saying where its users authenticate
// and where its customers' secrets live.
type registerProductRequest struct {
	Key              string   `json:"key" binding:"required,max=64"`
	DisplayName      string   `json:"displayName" binding:"required,max=200"`
	ZitadelProjectID string   `json:"zitadelProjectId" binding:"required,max=64"`
	MachineUserID    string   `json:"machineUserId" binding:"required,max=64"`
	OIDCClientID     string   `json:"oidcClientId" binding:"omitempty,max=200"`
	Hosts            []string `json:"hosts" binding:"omitempty,dive,hostname_port|hostname"`
	Identity         string   `json:"identity" binding:"required,oneof=zitadel gip"`
	TenantSecrets    string   `json:"tenantSecrets" binding:"required,oneof=openbao none"`
	Tenancy          string   `json:"tenancy" binding:"required"`
	Roles            []string `json:"roles" binding:"omitempty,dive,max=64"`
	DataResidency    string   `json:"dataResidency" binding:"omitempty,max=32"`
}

func (d Deps) registerProduct(c *gin.Context) {
	var req registerProductRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abort(c, http.StatusBadRequest, "the request body is not a valid product registration")
		return
	}
	product, err := d.Service.RegisterProduct(c.Request.Context(), principalOf(c), store.Product{
		Key: req.Key, DisplayName: req.DisplayName, ZitadelProjectID: req.ZitadelProjectID,
		MachineUserID: req.MachineUserID, OIDCClientID: req.OIDCClientID, Hosts: req.Hosts,
		Identity: store.Identity(req.Identity), TenantSecrets: store.TenantSecrets(req.TenantSecrets),
		Tenancy: store.Tenancy(req.Tenancy), Roles: req.Roles,
		DataResidency: orDefault(req.DataResidency, "in"), State: "active",
	})
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusCreated, product)
}

func (d Deps) acknowledgeManifests(c *gin.Context) {
	if err := d.Service.AcknowledgeProductManifests(c.Request.Context(), principalOf(c), c.Param("key")); err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- organizations -------------------------------------------------------

func (d Deps) listOrgs(c *gin.Context) {
	orgs, err := d.Service.Organizations(c.Request.Context(), principalOf(c))
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"organizations": orgs})
}

func (d Deps) createOrg(c *gin.Context) {
	var in onboarding.CreateOrgInput
	if err := c.ShouldBindJSON(&in); err != nil {
		abort(c, http.StatusBadRequest, "name and a valid ownerEmail are required")
		return
	}
	org, err := d.Service.CreateOrganization(c.Request.Context(), principalOf(c), in)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusCreated, org)
}

func (d Deps) getOrg(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	org, err := d.Service.Organization(c.Request.Context(), principalOf(c), id)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, org)
}

func (d Deps) setOrgState(state string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, ok := orgID(c)
		if !ok {
			return
		}
		if err := d.Service.SetOrganizationState(c.Request.Context(), principalOf(c), id, state); err != nil {
			respondErr(c, d.logger(), err)
			return
		}
		c.JSON(http.StatusOK, gin.H{"id": id, "state": state})
	}
}

// --- grants --------------------------------------------------------------

func (d Deps) listGrants(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	grants, err := d.Service.Grants(c.Request.Context(), principalOf(c), id)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"grants": grants})
}

func (d Deps) addGrant(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	var req struct {
		ProductKey string `json:"productKey" binding:"required,max=64"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		abort(c, http.StatusBadRequest, "productKey is required")
		return
	}
	if err := d.Service.AddGrant(c.Request.Context(), principalOf(c), id, req.ProductKey); err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"orgId": id, "productKey": req.ProductKey})
}

func (d Deps) revokeGrant(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	if err := d.Service.RevokeGrant(c.Request.Context(), principalOf(c), id, c.Param("productKey")); err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- domains -------------------------------------------------------------

func (d Deps) listDomains(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	domains, err := d.Service.Domains(c.Request.Context(), principalOf(c), id)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"domains": domains})
}

func (d Deps) addDomain(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	var req struct {
		Domain string `json:"domain" binding:"required,fqdn"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		abort(c, http.StatusBadRequest, "a fully qualified domain is required")
		return
	}
	domain, err := d.Service.AddDomain(c.Request.Context(), principalOf(c), id, req.Domain)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusCreated, domain)
}

func (d Deps) verifyDomain(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	if err := d.Service.VerifyDomain(c.Request.Context(), principalOf(c), id, c.Param("domain")); err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"domain": c.Param("domain"), "state": "verified"})
}

func (d Deps) removeDomain(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	if err := d.Service.RemoveDomain(c.Request.Context(), principalOf(c), id, c.Param("domain")); err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- identity providers --------------------------------------------------

func (d Deps) listIDPs(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	idps, err := d.Service.IDPs(c.Request.Context(), principalOf(c), id)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"idps": idps})
}

// createIDPRequest is the one request body carrying a customer secret. It is
// bound, forwarded to Zitadel, and dropped — never logged, never stored.
type createIDPRequest struct {
	Type         string   `json:"type" binding:"required,oneof=oidc okta entra google github saml ldap"`
	Name         string   `json:"name" binding:"required,max=200"`
	Issuer       string   `json:"issuer" binding:"omitempty,url"`
	ClientID     string   `json:"clientId" binding:"omitempty,max=400"`
	ClientSecret string   `json:"clientSecret" binding:"omitempty,max=2000"`
	TenantID     string   `json:"tenantId" binding:"omitempty,max=200"`
	MetadataXML  string   `json:"metadataXml" binding:"omitempty,max=100000"`
	Scopes       []string `json:"scopes" binding:"omitempty,dive,max=100"`
	AutoRegister bool     `json:"autoRegister"`
}

func (d Deps) createIDP(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	var req createIDPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		abort(c, http.StatusBadRequest, "the identity provider details are incomplete")
		return
	}
	idp, err := d.Service.CreateIDP(c.Request.Context(), principalOf(c), id, zitadel.IDPSpec{
		Type: req.Type, Name: req.Name, Issuer: req.Issuer, ClientID: req.ClientID,
		ClientSecret: req.ClientSecret, TenantID: req.TenantID, MetadataXML: req.MetadataXML,
		Scopes: req.Scopes, AutoRegister: req.AutoRegister,
	})
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusCreated, idp)
}

func (d Deps) deleteIDP(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	if err := d.Service.DeleteIDP(c.Request.Context(), principalOf(c), id, c.Param("idpId")); err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- members -------------------------------------------------------------

func (d Deps) listMembers(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	members, err := d.Service.Members(c.Request.Context(), principalOf(c), id)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"members": members})
}

func (d Deps) addMember(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	var req struct {
		Email string `json:"email" binding:"required,email"`
		Name  string `json:"name" binding:"omitempty,max=200"`
		Role  string `json:"role" binding:"omitempty,oneof=ORG_OWNER ORG_USER_MANAGER ORG_OWNER_VIEWER"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		abort(c, http.StatusBadRequest, "a valid email and role are required")
		return
	}
	member, err := d.Service.AddMember(c.Request.Context(), principalOf(c), id, req.Email, req.Name, req.Role)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusCreated, member)
}

func (d Deps) removeMember(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	if err := d.Service.RemoveMember(c.Request.Context(), principalOf(c), id, c.Param("userId")); err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- discovery -----------------------------------------------------------

// resolve is a POST because the email must not land in an access log or a
// browser history, and because a GET would be cacheable.
func (d Deps) resolve(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		abort(c, http.StatusBadRequest, "a valid email is required")
		return
	}
	res, err := d.Service.Resolve(c.Request.Context(), principalOf(c), req.Email)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, res)
}

// --- audit ---------------------------------------------------------------

func (d Deps) listAudit(c *gin.Context) {
	filter := store.AuditFilter{
		ProductKey: c.Query("productKey"),
		Actor:      c.Query("actor"),
		Action:     c.Query("action"),
	}
	// An orgId in the query narrows the read and moves it under the org guard,
	// so a caller cannot use it to reach an org they are not entitled to.
	if raw := c.Query("orgId"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			abort(c, http.StatusBadRequest, "orgId must be a UUID")
			return
		}
		filter.OrgID = &id
	}
	records, err := d.Service.Audit(c.Request.Context(), principalOf(c), filter, auditLimit(c))
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": records})
}

func auditLimit(c *gin.Context) int {
	limit, err := strconv.Atoi(c.Query("limit"))
	if err != nil {
		return 0
	}
	return limit
}

func (d Deps) listOrgAudit(c *gin.Context) {
	id, ok := orgID(c)
	if !ok {
		return
	}
	records, err := d.Service.Audit(c.Request.Context(), principalOf(c),
		store.AuditFilter{OrgID: &id}, auditLimit(c))
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": records})
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
