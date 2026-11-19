package customercontract

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const admissionScopeJSON = `{"tenantId":"11111111-1111-4111-8111-111111111111","subject":"ordinary-staff","generationId":"019f39c2-1738-7998-b5de-6d4214f614e0","requestId":"22222222-2222-4222-8222-222222222222"}`
const admissionTargetJSON = `{"tenantId":"33333333-3333-4333-8333-333333333333","employerKind":"hospital","employerId":"44444444-4444-4444-8444-444444444444","staffId":"55555555-5555-4555-8555-555555555555","staffVersion":7,"linkId":"019f39c2-1738-7998-b5de-6d4214f614e1","linkVersion":3,"subject":"ordinary-staff"}`

func TestStaffMFAAdmissionIsPrivateExactSubjectAndStrict(t *testing.T) {
	good := `{"scope":` + admissionScopeJSON + `,"target":` + admissionTargetJSON + `,"nativeSessionId":"signed-native-session"}`
	var in StaffMFAAdmissionInput
	require.NoError(t, json.Unmarshal([]byte(good), &in))
	require.NoError(t, in.Validate())
	require.NotEqual(t, in.Scope.TenantID, in.Target.TenantID, "legacy membership must not change credential ownership")
	require.NotContains(t, fmt.Sprintf("%+v", in), in.NativeSessionID)
	for _, bad := range []string{
		`null`, `{}`, good + ` {}`,
		strings.Replace(good, `"nativeSessionId":"signed-native-session"`, `"nativeSessionId":null`, 1),
		strings.Replace(good, `"nativeSessionId":"signed-native-session"`, `"nativeSessionId":"../foreign"`, 1),
		strings.Replace(good, `"nativeSessionId":"signed-native-session"`, `"nativeSessionId":""`, 1),
		strings.Replace(good, `"nativeSessionId"`, `"NativeSessionId"`, 1),
		strings.Replace(good, `"subject":"ordinary-staff"`, `"subject":"other-staff"`, 1),
		strings.Replace(good, `019f39c2-1738-7998-b5de-6d4214f614e0`, `11111111-1111-4111-8111-111111111111`, 1),
		strings.TrimSuffix(good, "}") + `,"reviewed":true}`,
		strings.TrimSuffix(good, "}") + `,"nativeSessionId":"other-session"}`,
	} {
		var value StaffMFAAdmissionInput
		require.Error(t, json.Unmarshal([]byte(bad), &value))
		require.Zero(t, value)
	}
}

func TestStaffMFAAdmissionReceiptAndSourceExposeNoSessionOrAdminSnapshot(t *testing.T) {
	var source StaffMFAAdmissionSource
	require.NoError(t, json.Unmarshal([]byte(`{"generationId":"019f39c2-1738-7998-b5de-6d4214f614e0","target":`+admissionTargetJSON+`}`), &source))
	var receipt StaffMFAAdmissionReceipt
	require.NoError(t, json.Unmarshal([]byte(`{"generationId":"019f39c2-1738-7998-b5de-6d4214f614e0"}`), &receipt))
	require.Equal(t, source.GenerationID, receipt.GenerationID)
	for _, bad := range []string{`null`, `{}`, `{"generationId":"11111111-1111-4111-8111-111111111111"}`, `{"generationId":"019f39c2-1738-7998-b5de-6d4214f614e0","nativeSessionId":"private"}`} {
		var value StaffMFAAdmissionReceipt
		require.Error(t, json.Unmarshal([]byte(bad), &value))
	}
}
