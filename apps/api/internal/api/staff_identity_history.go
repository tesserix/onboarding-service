package api

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/tesserix/onboarding-service/api/internal/onboarding"
)

func (d Deps) getStaffIdentityHistory(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	tenant, command, ok := staffIdentityIDs(c)
	if !ok {
		return
	}
	body, _ := c.Get(bodyKey)
	raw, _ := body.([]byte)
	actor, cursor, limit, err := staffHistoryQuery(c.Request.URL.RawQuery)
	if err != nil || len(raw) != 0 {
		abort(c, http.StatusBadRequest, "history requires actor_subject and optional limit/cursor, with no body")
		return
	}
	page, err := d.Service.StaffIdentityHistory(c.Request.Context(), principalOf(c), tenant, command, actor, cursor, limit)
	if err != nil {
		d.respondStaffIdentityErr(c, err)
		return
	}
	c.JSON(http.StatusOK, page)
}

func staffHistoryQuery(raw string) (string, string, int, error) {
	query, err := url.ParseQuery(raw)
	if err != nil || len(query["actor_subject"]) != 1 || query.Get("actor_subject") == "" {
		return "", "", 0, onboarding.ErrInvalid
	}
	for key, values := range query {
		if (key != "actor_subject" && key != "cursor" && key != "limit") || len(values) != 1 || values[0] == "" {
			return "", "", 0, onboarding.ErrInvalid
		}
	}
	limit := 20
	if value := query.Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 || strconv.Itoa(limit) != value {
			return "", "", 0, onboarding.ErrInvalid
		}
	}
	if len(query.Get("cursor")) > 43 {
		return "", "", 0, onboarding.ErrInvalid
	}
	return query.Get("actor_subject"), query.Get("cursor"), limit, nil
}
