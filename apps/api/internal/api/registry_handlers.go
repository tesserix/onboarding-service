package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/tesserix/onboarding-service/api/internal/authz"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
)

func (d Deps) onboardRegistryTenant(c *gin.Context) {
	if !requireMutationID(c) {
		return
	}
	var input onboarding.RegistryTenantInput
	if !decodeRegistryJSON(c, &input) {
		return
	}
	tenant, err := d.Service.OnboardRegistryTenant(c.Request.Context(), principalOf(c), input)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusCreated, tenant)
}

func (d Deps) listRegistryCredentials(c *gin.Context) {
	p, ok := registryCredentialActor(c)
	if !ok {
		return
	}
	credentials, err := d.Service.RegistryCredentials(c.Request.Context(), p)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"credentials": credentials})
}

func (d Deps) createRegistryCredential(c *gin.Context) {
	p, ok := registryCredentialActor(c)
	if !ok || !requireMutationID(c) {
		return
	}
	var input onboarding.RegistryCredentialInput
	if !decodeRegistryJSON(c, &input) {
		return
	}
	credential, err := d.Service.CreateRegistryCredential(c.Request.Context(), p, input)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusCreated, credential)
}

func (d Deps) rotateRegistryCredential(c *gin.Context) {
	p, ok := registryCredentialActor(c)
	if !ok || !requireMutationID(c) {
		return
	}
	id, err := uuid.Parse(c.Param("credentialId"))
	if err != nil {
		abort(c, http.StatusBadRequest, "credential id must be a uuid")
		return
	}
	credential, err := d.Service.RotateRegistryCredential(c.Request.Context(), p, id)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.Header("Pragma", "no-cache")
	c.JSON(http.StatusOK, credential)
}

func (d Deps) revokeRegistryCredential(c *gin.Context) {
	p, ok := registryCredentialActor(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("credentialId"))
	if err != nil {
		abort(c, http.StatusBadRequest, "credential id must be a uuid")
		return
	}
	if err := d.Service.RevokeRegistryCredential(c.Request.Context(), p, id); err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.Status(http.StatusNoContent)
}

func registryCredentialActor(c *gin.Context) (authz.Principal, bool) {
	p := principalOf(c)
	if p.Kind != authz.KindOrgAdmin || p.ExternalOrgID == "" {
		abort(c, http.StatusForbidden, "a verified tenant administrator is required")
		return authz.Principal{}, false
	}
	expected := strings.TrimSpace(c.GetHeader("X-Tesserix-Expected-Tenant"))
	if expected == "" {
		abort(c, http.StatusBadRequest, "X-Tesserix-Expected-Tenant is required")
		return authz.Principal{}, false
	}
	if expected != p.ExternalOrgID {
		abort(c, http.StatusForbidden, "tenant context does not match the actor token")
		return authz.Principal{}, false
	}
	return p, true
}

func requireMutationID(c *gin.Context) bool {
	key := strings.TrimSpace(c.GetHeader("Idempotency-Key"))
	if len(key) < 8 || len(key) > 200 {
		abort(c, http.StatusBadRequest, "Idempotency-Key must be between 8 and 200 characters")
		return false
	}
	return true
}

func decodeRegistryJSON(c *gin.Context, output any) bool {
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(output); err != nil {
		abort(c, http.StatusBadRequest, "request body is not valid Registry identity JSON")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		abort(c, http.StatusBadRequest, "request body must contain exactly one JSON object")
		return false
	}
	return true
}
