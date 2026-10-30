package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
)

func (d Deps) getStaffIdentityRecovery(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	body, _ := c.Get(bodyKey)
	raw, _ := body.([]byte)
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery || len(raw) != 0 {
		abort(c, http.StatusBadRequest, "recovery read does not accept query parameters or a request body")
		return
	}
	receipt, err := d.Service.StaffIdentityRecoveryReceipt(c.Request.Context(), principalOf(c), tenant, command)
	if err != nil {
		d.respondStaffIdentityErr(c, err)
		return
	}
	c.JSON(http.StatusOK, receipt)
}

func staffRecoveryRequestID(c *gin.Context) (uuid.UUID, bool) {
	request, err := uuid.Parse(c.Param("requestId"))
	if err != nil || request.Version() != 7 || request.Variant() != uuid.RFC4122 {
		abort(c, http.StatusBadRequest, "requestId must be a UUIDv7")
		return uuid.Nil, false
	}
	return request, true
}

func (d Deps) getStaffIdentityRecoveryRequest(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	request, ok := staffRecoveryRequestID(c)
	if !ok {
		return
	}
	body, _ := c.Get(bodyKey)
	raw, _ := body.([]byte)
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery || len(raw) != 0 {
		abort(c, http.StatusBadRequest, "request read does not accept query parameters or a request body")
		return
	}
	receipt, err := d.Service.StaffIdentityRecoveryRequest(c.Request.Context(), principalOf(c), tenant, command, request)
	if err != nil {
		d.respondStaffIdentityErr(c, err)
		return
	}
	c.JSON(http.StatusOK, receipt)
}

func (d Deps) proposeStaffIdentityRecovery(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	request, ok := staffRecoveryRequestID(c)
	if !ok {
		return
	}
	body, _ := c.Get(bodyKey)
	raw, _ := body.([]byte)
	fields, err := staffIdentityFields(raw, "requested_action", "reason", "evidence_reference")
	if err != nil {
		fields, err = staffIdentityFields(raw, "requested_action", "reason", "evidence_reference", "replacement_actor_subject")
	}
	if err != nil || c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		abort(c, http.StatusBadRequest, "proposal requires requested_action, reason and evidence_reference; replace also requires replacement_actor_subject; no query is allowed")
		return
	}
	receipt, err := d.Service.ProposeStaffIdentityRecovery(c.Request.Context(), principalOf(c), tenant, command, request, onboarding.StaffIdentityRecoveryProposal{Action: fields["requested_action"], Reason: fields["reason"], EvidenceReference: fields["evidence_reference"], ReplacementActorSubject: fields["replacement_actor_subject"]})
	if err != nil {
		d.respondStaffIdentityErr(c, err)
		return
	}
	c.JSON(http.StatusOK, receipt)
}
