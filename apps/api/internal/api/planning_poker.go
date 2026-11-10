package api

import (
	"github.com/gin-gonic/gin"
	"github.com/tesserix/onboarding-service/api/internal/authz"
	"net/http"
)

func (d Deps) registerPlanningPoker(r *gin.Engine) {
	g := r.Group("/internal/v1/planning-poker", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if len(c.Request.Header.Values("Authorization")) != 1 || d.PlanningPokerWorkloads == nil || d.PlanningPokerWorkloads.Verify(c.Request.Context(), c.GetHeader("Authorization")) != nil {
			abort(c, http.StatusUnauthorized, "planning poker workload credential required")
			return
		}
		c.Set(principalKey, authz.Principal{Kind: authz.KindProduct, ProductKey: "planning-poker", Subject: "system:serviceaccount:planning-poker:planning-poker-api"})
		c.Next()
	}, readBody(), newRateLimiter(10, 20).middleware(), d.idempotency())
	g.GET("/orgs", d.listOrgs)
	g.POST("/orgs", d.createOrg)
	g.GET("/orgs/:id/idps", d.listIDPs)
	g.POST("/orgs/:id/idps", d.createIDP)
	g.DELETE("/orgs/:id/idps/:idpId", d.deleteIDP)
	g.POST("/orgs/:id/members", d.addMember)
}
