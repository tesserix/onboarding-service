package customercontract

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const staffMFAIntentJSON = `{"commandId":"019f39c2-1738-7998-b5de-6d4214f614e0","expectedLinkId":"019f39c2-1738-7998-b5de-6d4214f614e1","expectedLinkVersion":3,"expectedStaffVersion":7,"reason":"Lost authenticator reported in person"}`

func TestStaffMFAResetIntentBindsExactReviewedSourceWithoutAccountAuthority(t *testing.T) {
	var input StaffMFAResetInput
	require.NoError(t, json.Unmarshal([]byte(staffMFAIntentJSON), &input))
	require.Equal(t, int64(3), input.ExpectedLinkVersion)
	require.Equal(t, int64(7), input.ExpectedStaffVersion)
	require.NotContains(t, fmt.Sprintf("%+v", input), input.Reason)
	for _, bad := range []string{
		`null`, `{}`, strings.Replace(staffMFAIntentJSON, `"expectedLinkVersion":3`, `"expectedLinkVersion":0`, 1),
		strings.Replace(staffMFAIntentJSON, `"expectedLinkVersion":3`, `"expectedLinkVersion":-1`, 1),
		strings.Replace(staffMFAIntentJSON, `"expectedLinkVersion":3`, `"expectedLinkVersion":9007199254740992`, 1),
		strings.Replace(staffMFAIntentJSON, `"expectedStaffVersion":7`, `"expectedStaffVersion":7.1`, 1),
		strings.Replace(staffMFAIntentJSON, `"expectedStaffVersion":7`, `"expectedStaffVersion":null`, 1),
		strings.Replace(staffMFAIntentJSON, `"reason":"Lost authenticator reported in person"`, `"reason":" "`, 1),
		strings.Replace(staffMFAIntentJSON, `"reason":"Lost authenticator reported in person"`, `"reason":"lost\ndevice"`, 1),
		strings.Replace(staffMFAIntentJSON, `"reason":"Lost authenticator reported in person"`, `"reason":"`+strings.Repeat("x", 501)+`"`, 1),
		strings.Replace(staffMFAIntentJSON, `"commandId"`, `"CommandId"`, 1),
		strings.Replace(staffMFAIntentJSON, `019f39c2-1738-7998-b5de-6d4214f614e0`, `efb09160-2825-445e-af8d-a1b5f61f1e02`, 1),
		strings.Replace(staffMFAIntentJSON, `019f39c2-1738-7998-b5de-6d4214f614e1`, `efb09160-2825-445e-af8d-a1b5f61f1e02`, 1),
		strings.TrimSuffix(staffMFAIntentJSON, "}") + `,"subject":"chosen-by-browser"}`,
		strings.TrimSuffix(staffMFAIntentJSON, "}") + `,"expectedLinkVersion":4}`,
		staffMFAIntentJSON + ` {}`,
	} {
		var input StaffMFAResetInput
		require.Error(t, json.Unmarshal([]byte(bad), &input))
		require.Zero(t, input)
	}
}

func TestStaffMFARecoveryRequestRequiresMatchingSourceAndDistinctActor(t *testing.T) {
	const binding = `{"tenantId":"019f39c2-1738-7998-b5de-6d4214f614e2","employerKind":"hospital","employerId":"019f39c2-1738-7998-b5de-6d4214f614e3","staffId":"019f39c2-1738-7998-b5de-6d4214f614e4","staffVersion":7,"linkId":"019f39c2-1738-7998-b5de-6d4214f614e1","linkVersion":3,"subject":"ordinary-staff"}`
	good := `{"actorSubject":"tenant-admin","target":` + binding + `,"intent":` + staffMFAIntentJSON + `}`
	for _, kind := range []string{"hospital", "pharmacy", "supplier"} {
		var request StaffMFARecoveryRequest
		require.NoError(t, json.Unmarshal([]byte(strings.Replace(good, `"hospital"`, `"`+kind+`"`, 1)), &request))
		require.Equal(t, "ordinary-staff", request.Target.Subject)
		require.NotContains(t, fmt.Sprintf("%#v", request), request.Intent.Reason)
	}
	for _, bad := range []string{
		strings.Replace(good, `"actorSubject":"tenant-admin"`, `"actorSubject":"ordinary-staff"`, 1),
		strings.Replace(good, `"linkVersion":3`, `"linkVersion":2`, 1),
		strings.Replace(good, `"staffVersion":7`, `"staffVersion":8`, 1),
		strings.Replace(good, `"linkId":"019f39c2-1738-7998-b5de-6d4214f614e1"`, `"linkId":"019f39c2-1738-7998-b5de-6d4214f614e5"`, 1),
		strings.Replace(good, `"employerKind":"hospital"`, `"employerKind":"department"`, 1),
		strings.Replace(good, `"subject":"ordinary-staff"`, `"subject":"../foreign"`, 1),
		strings.Replace(good, `"subject":"ordinary-staff"`, `"subject":"ordinary-staff","subject":"other"`, 1),
		strings.TrimSuffix(good, "}") + `,"approved":true}`,
	} {
		var request StaffMFARecoveryRequest
		require.Error(t, json.Unmarshal([]byte(bad), &request))
		require.Zero(t, request)
	}
}
