package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
)

func (d Deps) enrolOrg(c *gin.Context) {
	var in onboarding.EnrolOrgInput
	if err := c.ShouldBindJSON(&in); err != nil {
		abort(c, http.StatusBadRequest, "an existing tenant binding, product, name and reason are required")
		return
	}
	org, err := d.Service.EnrolOrganization(c.Request.Context(), principalOf(c), in)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, org)
}
