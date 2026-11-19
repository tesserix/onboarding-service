package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
)

func (d Deps) staffMFAAdmission(c *gin.Context, source bool) {
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 8192))
	if err != nil || c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		abort(c, 400, "one bounded private recovery request is required")
		return
	}
	var read customercontract.StaffMFAAdmissionRead
	var input customercontract.StaffMFAAdmissionInput
	if source {
		err = json.Unmarshal(raw, &read)
	} else {
		err = json.Unmarshal(raw, &input)
	}
	if err != nil {
		abort(c, 400, "invalid private recovery request")
		return
	}
	if d.Service == nil {
		abort(c, 503, "recovery admission unavailable; reconcile the same generation")
		return
	}
	var result any
	if source {
		result, err = d.Service.StaffMFAAdmissionSource(c.Request.Context(), read)
	} else {
		result, err = d.Service.AdmitStaffMFASession(c.Request.Context(), input)
	}
	if errors.Is(err, onboarding.ErrMFAAuthenticationRequired) {
		abort(c, http.StatusUnauthorized, "fresh recovery authentication is required")
		return
	}
	if errors.Is(err, onboarding.ErrUnavailable) {
		d.logger().Error("recovery admission unavailable", "requestId", c.GetString(requestIDKey))
		abort(c, 503, "recovery admission unavailable; reconcile the same generation")
		return
	}
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, result)
}
