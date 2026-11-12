package api

import (
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

func (d Deps) startAccountDiscovery(c *gin.Context) {
	var in customercontract.DiscoveryStart
	if !decodeHospital(c, &in) {
		return
	}
	if d.Service == nil {
		abort(c, 503, "identity unavailable")
		return
	}
	if err := d.Service.StartAccountDiscovery(c.Request.Context(), in); err != nil {
		if errors.Is(err, store.ErrDiscoveryLimited) {
			c.Header("Retry-After", "3600")
			abort(c, 429, "try again later")
			return
		}
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(202, customercontract.DiscoveryAccepted{State: "check_email"})
}
func (d Deps) verifyAccountDiscovery(c *gin.Context) {
	var in customercontract.DiscoveryProof
	if !decodeHospital(c, &in) {
		return
	}
	if d.Service == nil {
		abort(c, 503, "identity unavailable")
		return
	}
	if err := d.Service.VerifyAccountDiscovery(c.Request.Context(), in); err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(200, customercontract.DiscoveryVerified{State: "verified"})
}
func (d Deps) accountChoices(c *gin.Context) {
	var in customercontract.DiscoveryAccess
	if !decodeHospital(c, &in) {
		return
	}
	if d.Service == nil {
		abort(c, 503, "identity unavailable")
		return
	}
	result, err := d.Service.AccountChoices(c.Request.Context(), in)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(200, result)
}
