package api

import (
	"encoding/hex"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
	"net/http"
)

func (d Deps) registerCustomers(r *gin.Engine) {
	group := r.Group("/internal/v1/customer-signups", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if len(c.Request.Header.Values("Authorization")) != 1 || d.CustomerWorkloads == nil || d.CustomerWorkloads.Verify(c.Request.Context(), c.GetHeader("Authorization")) != nil {
			abort(c, http.StatusUnauthorized, "customer provisioning credential required")
			return
		}
		c.Next()
	})
	group.POST("", d.startCustomer)
	group.GET("/:id", d.getCustomer)
	group.GET("/:id/users/:userID", d.getCustomerForOwner)
	group.POST("/:id/existing-owner", d.continueCustomer)
	group.POST("/verify-email", func(c *gin.Context) { d.proveCustomer(c, false) })
	group.POST("/password", func(c *gin.Context) { d.proveCustomer(c, true) })
	group.POST("/email-link", func(c *gin.Context) { d.deliverCustomer(c, false) })
	group.POST("/password-link", func(c *gin.Context) { d.deliverCustomer(c, true) })
	group.POST("/login-target", d.customerLoginTarget)
	group.POST("/identity-projection", d.customerIdentityProjection)
}

func (d Deps) customerLoginTarget(c *gin.Context) {
	var in customercontract.LoginTargetInput
	if !decodeHospital(c, &in) {
		return
	}
	if d.Service == nil {
		abort(c, 503, "identity unavailable")
		return
	}
	result, err := d.Service.CustomerLoginTarget(c.Request.Context(), in)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, result)
}
func customerHash(c *gin.Context) ([]byte, bool) {
	if len(c.Request.Header.Values("X-Signup-Receipt-Hash")) != 1 {
		abort(c, 400, "invalid signup receipt")
		return nil, false
	}
	hash, err := hex.DecodeString(c.GetHeader("X-Signup-Receipt-Hash"))
	if err != nil || len(hash) != 32 {
		abort(c, 400, "invalid signup receipt")
		return nil, false
	}
	return hash, true
}
func (d Deps) startCustomer(c *gin.Context) {
	hash, ok := customerHash(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.GetHeader("Idempotency-Key"))
	if err != nil || id == uuid.Nil || id.String() != c.GetHeader("Idempotency-Key") {
		abort(c, 400, "invalid signup command")
		return
	}
	var in customercontract.Signup
	if !decodeHospital(c, &in) {
		return
	}
	receipt, err := d.Service.StartCustomer(c.Request.Context(), id, hash, in)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusAccepted, receipt)
}
func (d Deps) getCustomer(c *gin.Context) {
	hash, ok := customerHash(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil || id.String() != c.Param("id") {
		abort(c, 404, "not found")
		return
	}
	receipt, err := d.Service.Customer(c.Request.Context(), id, hash)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, receipt)
}

func (d Deps) getCustomerForOwner(c *gin.Context) {
	hash, ok := customerHash(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil || id.String() != c.Param("id") {
		abort(c, 404, "not found")
		return
	}
	receipt, err := d.Service.CustomerForOwner(c.Request.Context(), id, hash, c.Param("userID"))
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, receipt)
}

func (d Deps) proveCustomer(c *gin.Context, password bool) {
	hash, ok := customerHash(c)
	if !ok {
		return
	}
	var in customercontract.Proof
	if !decodeHospital(c, &in) {
		return
	}
	id, receipt, err := d.Service.ProveCustomer(c.Request.Context(), in, hash, password)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.Header("X-Signup-Command", id.String())
	c.JSON(http.StatusOK, receipt)
}
func (d Deps) deliverCustomer(c *gin.Context, password bool) {
	var in customercontract.Delivery
	if !decodeHospital(c, &in) {
		return
	}
	var id uuid.UUID
	var hash []byte
	if in.UserID == "" {
		var ok bool
		hash, ok = customerHash(c)
		if !ok {
			return
		}
		var err error
		id, err = uuid.Parse(c.GetHeader("Idempotency-Key"))
		if err != nil {
			abort(c, 400, "invalid signup command")
			return
		}
	}
	receipt, err := d.Service.DeliverCustomer(c.Request.Context(), id, hash, in, password)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusAccepted, receipt)
}

func (d Deps) continueCustomer(c *gin.Context) {
	hash, ok := customerHash(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil || id == uuid.Nil || id.String() != c.Param("id") {
		abort(c, 400, "invalid signup command")
		return
	}
	var in customercontract.ExistingOwnerInput
	if !decodeHospital(c, &in) {
		return
	}
	receipt, err := d.Service.ContinueCustomer(c.Request.Context(), id, hash, in)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, receipt)
}

func (d Deps) customerIdentityProjection(c *gin.Context) {
	var in customercontract.IdentityProjectionInput
	if !decodeHospital(c, &in) {
		return
	}
	if d.Service == nil {
		abort(c, 503, "identity unavailable")
		return
	}
	result, err := d.Service.CustomerIdentityProjection(c.Request.Context(), in)
	if err != nil {
		respondErr(c, d.logger(), err)
		return
	}
	c.JSON(http.StatusOK, result)
}
