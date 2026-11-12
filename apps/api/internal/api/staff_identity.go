package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"slices"
	"unicode/utf8"

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
	fields, err := staffIdentityFields(raw, "actor_subject")
	return fields["actor_subject"], err
}

func staffIdentityFields(raw []byte, allowed ...string) (map[string]string, error) {
	if len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) {
		return nil, onboarding.ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, onboarding.ErrInvalid
	}
	fields := make(map[string]string, len(allowed))
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || !slices.Contains(allowed, name) {
			return nil, onboarding.ErrInvalid
		}
		if _, seen := fields[name]; seen {
			return nil, onboarding.ErrInvalid
		}
		var value string
		if decoder.Decode(&value) != nil || value == "" {
			return nil, onboarding.ErrInvalid
		}
		fields[name] = value
	}
	end, err := decoder.Token()
	if err != nil || end != json.Delim('}') || len(fields) != len(allowed) {
		return nil, onboarding.ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, onboarding.ErrInvalid
	}
	return fields, nil
}

func (d Deps) createStaffIdentity(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	body, _ := c.Get(bodyKey)
	raw, _ := body.([]byte)
	fields, err := staffIdentityFields(raw, "actor_subject", "email", "given_name", "family_name", "reason")
	if err != nil || c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery {
		abort(c, http.StatusBadRequest, "creation requires exactly actor_subject, email, given_name, family_name and reason, with no query")
		return
	}
	receipt, err := d.Service.CreateStaffIdentity(c.Request.Context(), principalOf(c), tenant, command, onboarding.StaffIdentityCreateInput{ActorSubject: fields["actor_subject"], Email: fields["email"], GivenName: fields["given_name"], FamilyName: fields["family_name"], Reason: fields["reason"]})
	if err != nil {
		d.respondStaffIdentityErr(c, err)
		return
	}
	status := http.StatusAccepted
	if receipt.CommandState == "created" {
		status = http.StatusOK
	}
	c.JSON(status, receipt)
}

func (d Deps) respondStaffIdentityErr(c *gin.Context, err error) {
	if errors.Is(err, onboarding.ErrUnavailable) {
		d.logger().Error("staff identity proof unavailable", "path", c.FullPath(), "requestId", c.GetString(requestIDKey))
		abort(c, http.StatusServiceUnavailable, "identity proof is unavailable; retain the command id and recover its exact state")
		return
	}
	respondErr(c, d.logger(), err)
}
