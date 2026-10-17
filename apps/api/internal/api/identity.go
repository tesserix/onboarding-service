package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (d Deps) getOrgIdentity(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if c.Request.URL.RawQuery != "" {
		abort(c, http.StatusBadRequest, "identity verification takes no query parameters")
		return
	}
	id, ok := orgID(c)
	if !ok {
		return
	}
	identity, err := d.Service.ProductIdentity(c.Request.Context(), principalOf(c), id)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, identity)
}
