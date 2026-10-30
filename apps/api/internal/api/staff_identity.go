package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
)

func (d Deps) getStaffIdentity(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	body, _ := c.Get(bodyKey)
	raw, _ := body.([]byte)
	if err != nil || len(query) != 1 || len(query["actor_subject"]) != 1 || len(raw) != 0 {
		abort(c, http.StatusBadRequest, "exactly one initiating actor query is required and no request body is allowed")
		return
	}
	receipt, err := d.Service.StaffIdentityReceipt(c.Request.Context(), principalOf(c), tenant, command, query.Get("actor_subject"))
	if err != nil {
		d.respondStaffIdentityErr(c, err)
		return
	}
	c.JSON(http.StatusOK, receipt)
}

func staffIdentityIDs(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	tenant, ok := orgID(c)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	command, err := uuid.Parse(c.Param("commandId"))
	if tenant == uuid.Nil || err != nil || command.Version() != 7 || command.Variant() != uuid.RFC4122 {
		abort(c, http.StatusBadRequest, "command id must be a UUIDv7")
		return uuid.Nil, uuid.Nil, false
	}
	return tenant, command, true
}

func (d Deps) observeStaffIdentity(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	body, _ := c.Get(bodyKey)
	raw, _ := body.([]byte)
	actor, err := staffObservationActor(raw)
	if err != nil || c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		abort(c, http.StatusBadRequest, "observation requires only the initiating actor and no query parameters")
		return
	}
	receipt, err := d.Service.ObserveStaffIdentity(c.Request.Context(), principalOf(c), tenant, command, actor)
	if err != nil {
		d.respondStaffIdentityErr(c, err)
		return
	}
	c.JSON(http.StatusOK, receipt)
}

func staffObservationActor(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return "", onboarding.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return "", onboarding.ErrInvalid
	}
	actor, seen := "", false
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil || key != "actor_subject" || seen || decoder.Decode(&actor) != nil || actor == "" {
			return "", onboarding.ErrInvalid
		}
		seen = true
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || !seen {
		return "", onboarding.ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return "", onboarding.ErrInvalid
	}
	return actor, nil
}

func (d Deps) respondStaffIdentityErr(c *gin.Context, err error) {
	if errors.Is(err, onboarding.ErrUnavailable) {
		d.logger().Error("staff identity proof unavailable", "path", c.FullPath(), "requestId", c.GetString(requestIDKey))
		abort(c, http.StatusServiceUnavailable, "identity proof is unavailable; retain the command id and recover its exact state")
		return
	}
	respondErr(c, d.logger(), err)
}
