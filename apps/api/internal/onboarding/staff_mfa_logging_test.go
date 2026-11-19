package onboarding

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/tesserix/onboarding-service/api/customercontract"
)

func TestUnavailableStaffMFAPreparationLogsOnlyTheStage(t *testing.T) {
	var output bytes.Buffer
	service := New(nil, nil, nil, slog.New(slog.NewJSONHandler(&output, nil)))
	link := uuid.Must(uuid.NewV7()).String()
	input := customercontract.StaffMFARecoveryRequest{
		ActorSubject: "private-actor",
		Target: customercontract.StaffMFARecoveryTarget{
			TenantID: uuid.NewString(), EmployerKind: "hospital", EmployerID: uuid.NewString(), StaffID: uuid.NewString(),
			StaffVersion: 1, LinkID: link, LinkVersion: 1, Subject: "private-target",
		},
		Intent: customercontract.StaffMFAResetInput{
			CommandID: uuid.Must(uuid.NewV7()).String(), ExpectedLinkID: link, ExpectedLinkVersion: 1,
			ExpectedStaffVersion: 1, Reason: "private-reason",
		},
	}
	require.NoError(t, input.Validate())
	_, err := service.PrepareStaffMFARecovery(t.Context(), product("helivanta"), input)
	require.ErrorIs(t, err, ErrUnavailable)
	var record map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(output.Bytes()), &record))
	require.Equal(t, "Staff MFA recovery preparation unavailable", record["msg"])
	require.Equal(t, "scope", record["stage"])
	for _, private := range []string{input.ActorSubject, input.Target.Subject, input.Target.TenantID, input.Intent.CommandID, input.Intent.Reason} {
		require.NotContains(t, output.String(), private)
	}
}
