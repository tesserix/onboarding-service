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
	result, err := d.Service.StartPasswordRecovery(c.Request.Context(), in)
	if err != nil {
		if errors.Is(err, store.ErrPasswordRecoveryLimited) {
			c.Header("Retry-After", "3600")
			abort(c, 429, "try again later")
			return
		}
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(202, result)
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

func (d Deps) readPasswordRecovery(c *gin.Context) {
	var in customercontract.PasswordRecoveryRead
	if !decodeHospital(c, &in) {
		return
	}
	if d.Service == nil {
		abort(c, 503, "identity unavailable")
		return
	}
	result, err := d.Service.ReadPasswordRecovery(c.Request.Context(), in)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(200, result)
}

func (d Deps) completePasswordRecovery(c *gin.Context) {
	var in customercontract.PasswordRecoveryPassword
	if !decodeHospital(c, &in) {
		return
	}
	if d.Service == nil {
		abort(c, 503, "identity unavailable")
		return
	}
	result, err := d.Service.CompletePasswordRecovery(c.Request.Context(), in)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(200, result)
}

func (d Deps) preparePasswordRecovery(c *gin.Context) {
	var in customercontract.PasswordRecoveryIntent
	if !decodeHospital(c, &in) {
		return
	}
	if d.Service == nil {
		abort(c, 503, "identity unavailable")
		return
	}
	result, err := d.Service.PreparePasswordRecovery(c.Request.Context(), in)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(200, result)
}

func (d Deps) cancelPasswordRecovery(c *gin.Context) {
	var in customercontract.PasswordRecoveryIntent
	if !decodeHospital(c, &in) {
		return
	}
	if d.Service == nil {
		abort(c, 503, "identity unavailable")
		return
	}
	result, err := d.Service.CancelPasswordRecovery(c.Request.Context(), in)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(200, result)
}
