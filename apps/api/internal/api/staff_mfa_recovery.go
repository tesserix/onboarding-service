package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
)

func staffMFABody(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > 8192 || !utf8.Valid(raw) || json.Unmarshal(raw, out) != nil {
		return onboarding.ErrInvalid
	}
	return nil
}

func staffMFARaw(c *gin.Context) []byte {
	body, _ := c.Get(bodyKey)
	raw, _ := body.([]byte)
	return raw
}

func (d Deps) prepareStaffMFARecovery(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	var input customercontract.StaffMFARecoveryRequest
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery || staffMFABody(staffMFARaw(c), &input) != nil || input.Target.TenantID != tenant.String() || input.Intent.CommandID != command.String() {
		abort(c, http.StatusBadRequest, "an exact reviewed Staff recovery intent matching the path is required")
		return
	}
	receipt, err := d.Service.PrepareStaffMFARecovery(c.Request.Context(), principalOf(c), input)
	if err != nil {
		d.respondStaffIdentityErr(c, err)
		return
	}
	c.JSON(http.StatusOK, receipt)
}

func (d Deps) dispatchStaffMFARecovery(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	var input customercontract.StaffMFADispatchRequest
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery || staffMFABody(staffMFARaw(c), &input) != nil || input.Request.Target.TenantID != tenant.String() || input.Request.Intent.CommandID != command.String() {
		abort(c, http.StatusBadRequest, "the exact recovery intent and matching application fence are required")
		return
	}
	receipt, err := d.Service.DispatchStaffMFARecovery(c.Request.Context(), principalOf(c), input.Request, input.ApplicationFenceID)
	if err != nil {
		d.respondStaffIdentityErr(c, err)
		return
	}
	status := http.StatusOK
	if receipt.State == "processing" || receipt.State == "unconfirmed" {
		status = http.StatusAccepted
	}
	c.JSON(status, receipt)
}

func (d Deps) cancelStaffMFARecovery(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	var input customercontract.StaffMFARecoveryRequest
	if c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery || staffMFABody(staffMFARaw(c), &input) != nil || input.Target.TenantID != tenant.String() || input.Intent.CommandID != command.String() {
		abort(c, http.StatusBadRequest, "the exact undispatched recovery intent matching the path is required")
		return
	}
	receipt, err := d.Service.CancelStaffMFARecovery(c.Request.Context(), principalOf(c), input)
	if err != nil {
		d.respondStaffIdentityErr(c, err)
		return
	}
	c.JSON(http.StatusOK, receipt)
}

func (d Deps) getStaffMFARecovery(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil || len(query) != 1 || len(query["actor_subject"]) != 1 || query.Get("actor_subject") == "" || len(staffMFARaw(c)) != 0 {
		abort(c, http.StatusBadRequest, "exactly one initiating actor query and no body are required")
		return
	}
	snapshot, err := d.Service.ReadStaffMFARecovery(c.Request.Context(), principalOf(c), tenant, command, query.Get("actor_subject"))
	if err != nil {
		d.respondStaffIdentityErr(c, err)
		return
	}
	c.JSON(http.StatusOK, snapshot)
}
