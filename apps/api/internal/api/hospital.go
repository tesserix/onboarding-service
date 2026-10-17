package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
)

type WorkloadVerifier interface {
	Verify(context.Context, string) error
}

func (d Deps) startHospital(c *gin.Context) {
	var in onboarding.StartHospitalInput
	if !decodeHospital(c, &in) {
		return
	}
	result, e := d.Service.StartHospital(c.Request.Context(), principalOf(c), in, c.GetHeader("Idempotency-Key"))
	if e != nil {
		respondErr(c, d.logger(), e)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusAccepted, result)
}
func (d Deps) getHospital(c *gin.Context) {
	result, e := d.Service.Hospital(c.Request.Context(), principalOf(c), c.Query("product"), c.Param("tenantId"))
	if e != nil {
		respondErr(c, d.logger(), e)
		return
	}
	state := "pending"
	if onboarding.HospitalReady(result) {
		state = "complete"
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"state": state, "onboarding": result})
}
func (d Deps) completeHospital(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if d.HospitalWorkloads == nil || d.HospitalWorkloads.Verify(c.Request.Context(), c.GetHeader("Authorization")) != nil {
		abort(c, http.StatusUnauthorized, "provisioning credential required")
		return
	}
	var in onboarding.CompleteHospitalInput
	if !decodeHospital(c, &in) {
		return
	}
	if e := d.Service.CompleteHospital(c.Request.Context(), in); e != nil {
		respondErr(c, d.logger(), e)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tenantId": in.TenantID, "organizationId": in.OrganizationID, "state": "complete"})
}
func decodeHospital(c *gin.Context, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 8192))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || decoder.Decode(new(any)) != io.EOF {
		abort(c, http.StatusBadRequest, "invalid hospital onboarding request")
		return false
	}
	return true
}
