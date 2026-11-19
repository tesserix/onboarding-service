//go:build identityfixture

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/google/uuid"
)

const (
	fixtureMFAHospitalTenant = "11111111-1111-7111-8111-111111111111"
	fixtureMFAPharmacyTenant = "22222222-2222-7222-8222-222222222222"
)

func fixtureTenantIDs(mfa bool) []string {
	ids := []string{"11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"}
	if mfa {
		ids = append(ids, fixtureMFAHospitalTenant, fixtureMFAPharmacyTenant)
	}
	return ids
}

func fixtureMFAHandler(next http.Handler, enabled bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !enabled {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/internal/v1/customer-signups/staff-mfa-recovery/admit" {
			next.ServeHTTP(w, r)
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) == 5 && parts[0] == "" && parts[1] == "v1" && parts[2] == "orgs" && parts[4] == "identity" && r.Method == http.MethodGet && slices.Contains(fixtureTenantIDs(true), parts[3]) {
			next.ServeHTTP(w, r)
			return
		}
		if len(parts) != 6 && len(parts) != 7 {
			http.NotFound(w, r)
			return
		}
		if parts[0] != "" || parts[1] != "v1" || parts[2] != "orgs" || parts[4] != "staff-mfa-recoveries" || parts[3] != fixtureMFAHospitalTenant && parts[3] != fixtureMFAPharmacyTenant {
			http.NotFound(w, r)
			return
		}
		command, err := uuid.Parse(parts[5])
		if err != nil || command.Version() != 7 || command.Variant() != uuid.RFC4122 || command.String() != parts[5] {
			http.NotFound(w, r)
			return
		}
		if len(parts) == 6 && (r.Method == http.MethodGet || r.Method == http.MethodPut) || len(parts) == 7 && r.Method == http.MethodPost && (parts[6] == "dispatch" || parts[6] == "cancel") {
			next.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

func verifyFixtureMachine(ctx context.Context, client *http.Client, token, subject, org string) error {
	refused := errors.New("local native fixture machine proof unavailable")
	if ctx.Err() != nil || client == nil || len(token) < 1 || len(token) > 8192 || !fixtureMachineID.MatchString(subject) || org == "" {
		return refused
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, localIssuer+"/v2/users/"+subject, nil)
	if err != nil {
		return refused
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return refused
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		return refused
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil || len(raw) > 8192 {
		return refused
	}
	var payload struct {
		User struct {
			UserID  string `json:"userId"`
			State   string `json:"state"`
			Details struct {
				ResourceOwner string `json:"resourceOwner"`
			} `json:"details"`
			Machine *json.RawMessage `json:"machine"`
			Human   *json.RawMessage `json:"human"`
		} `json:"user"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.User.UserID != subject || payload.User.State != "USER_STATE_ACTIVE" || payload.User.Details.ResourceOwner != org || payload.User.Machine == nil || payload.User.Human != nil {
		return refused
	}
	var machine map[string]json.RawMessage
	if json.Unmarshal(*payload.User.Machine, &machine) != nil || machine == nil {
		return refused
	}
	return nil
}

var fixtureMachineID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)
