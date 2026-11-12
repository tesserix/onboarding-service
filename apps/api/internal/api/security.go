package api

import (
	"github.com/gin-gonic/gin"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"net/http"
)

func (d Deps) registerOwnerSecurity(r *gin.Engine) {
	group := r.Group("/internal/v1/owner-security", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if len(c.Request.Header.Values("Authorization")) != 1 || d.OwnerSecurityWorkloads == nil || d.OwnerSecurityWorkloads.Verify(c.Request.Context(), c.GetHeader("Authorization")) != nil {
			abort(c, http.StatusUnauthorized, "owner security workload required")
			return
		}
		c.Next()
	})
	group.POST("/read", func(c *gin.Context) { d.ownerSecurity(c, false) })
	group.POST("/reconcile", func(c *gin.Context) { d.ownerSecurity(c, true) })
}
func (d Deps) ownerSecurity(c *gin.Context, apply bool) {
	var in customercontract.SecurityRequest
	if !decodeHospital(c, &in) {
		return
	}
	view, err := d.Service.OwnerSecurity(c.Request.Context(), in, apply)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	status := http.StatusOK
	if apply {
		status = http.StatusAccepted
	}
	c.JSON(status, view)
}
