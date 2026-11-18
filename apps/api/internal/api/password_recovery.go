package api

import (
	"errors"

	"github.com/gin-gonic/gin"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

func (d Deps) startPasswordRecovery(c *gin.Context) {
	var in customercontract.PasswordRecoveryStart
	if !decodeHospital(c, &in) {
		return
	}
	if d.Service == nil {
		abort(c, 503, "identity unavailable")
		return
	}
	if err := d.Service.StartPasswordRecovery(c.Request.Context(), in); err != nil {
		if errors.Is(err, store.ErrPasswordRecoveryLimited) {
			c.Header("Retry-After", "3600")
			abort(c, 429, "try again later")
			return
		}
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(202, customercontract.PasswordRecoveryAccepted{State: "check_email"})
}

func (d Deps) verifyPasswordRecovery(c *gin.Context) {
	var in customercontract.PasswordRecoveryProof
	if !decodeHospital(c, &in) {
		return
	}
	if d.Service == nil {
		abort(c, 503, "identity unavailable")
		return
	}
	if err := d.Service.VerifyPasswordRecovery(c.Request.Context(), in); err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(200, customercontract.PasswordRecoveryVerified{State: "verified"})
}
