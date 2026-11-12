package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
	"github.com/tesserix/onboarding-service/api/internal/store"
)

func (d Deps) staffRecipient(action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		keys := []string{"invitation_id", "ticket"}
		switch action {
		case "email":
			keys = append(keys, "code")
		case "password":
			keys = append(keys, "code", "password")
		case "resend":
			keys = append(keys, "delivery_id")
		}
		body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 4096))
		if err != nil || c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
			abort(c, http.StatusBadRequest, "invalid setup request")
			return
		}
		fields, ok := staffIdentityFields(body, keys...)
		if ok != nil {
			abort(c, http.StatusBadRequest, "invalid setup request")
			return
		}
		in := onboarding.StaffRecipientInput{Ticket: fields["ticket"], Code: fields["code"], Password: fields["password"]}
		in.InvitationID, err = uuid.Parse(fields["invitation_id"])
		if err != nil {
			abort(c, http.StatusBadRequest, "invalid setup request")
			return
		}
		if action == "resend" {
			deliveryID, parseErr := uuid.Parse(fields["delivery_id"])
			err = parseErr
			in.DeliveryID = &deliveryID
			if err != nil {
				abort(c, http.StatusBadRequest, "invalid setup request")
				return
			}
		}
		if in.Validate(action) != nil {
			abort(c, http.StatusBadRequest, "invalid setup request")
			return
		}
		if d.Service == nil {
			abort(c, http.StatusServiceUnavailable, "setup temporarily unavailable")
			return
		}
		out, err := d.Service.StaffRecipient(c.Request.Context(), in, action)
		if err != nil {
			d.respondStaffInvitationErr(c, err)
			return
		}
		if out.Validate() != nil {
			abort(c, http.StatusServiceUnavailable, "setup temporarily unavailable")
			return
		}
		c.JSON(http.StatusOK, out)
	}
}

func (d Deps) deliverStaffInvitation(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("invitationId"))
	if err != nil {
		abort(c, http.StatusBadRequest, "invalid delivery id")
		return
	}
	body, _ := c.Get(bodyKey)
	raw, _ := body.([]byte)
	fields, valid := staffIdentityFields(raw, "actor_subject")
	if valid != nil || c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		abort(c, http.StatusBadRequest, "invalid delivery request")
		return
	}
	out, err := d.Service.DeliverStaffInvitation(c.Request.Context(), principalOf(c), tenant, command, id, fields["actor_subject"])
	if err != nil {
		d.respondStaffInvitationErr(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}

func (d Deps) respondStaffInvitationErr(c *gin.Context, err error) {
	var limited store.StaffInvitationRateLimit
	if errors.As(err, &limited) {
		c.Header("Retry-After", strconv.Itoa(limited.RetryAfterSeconds))
		abort(c, http.StatusTooManyRequests, "staff setup resend limit reached")
		return
	}
	d.respondStaffIdentityErr(c, err)
}
