package customercontract

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const admissionScopeJSON = `{"tenantId":"11111111-1111-4111-8111-111111111111","subject":"ordinary-staff","generationId":"019f39c2-1738-7998-b5de-6d4214f614e0","requestId":"22222222-2222-4222-8222-222222222222"}`

func TestStaffMFAAdmissionIsPrivateExactSubjectAndStrict(t *testing.T) {
	good := `{"scope":` + admissionScopeJSON + `,"nativeSessionId":"signed-native-session"}`
	var in StaffMFAAdmissionInput
	require.NoError(t, json.Unmarshal([]byte(good), &in))
	require.NoError(t, in.Validate())
	require.Equal(t, "ordinary-staff", in.Scope.Subject)
	require.NotContains(t, fmt.Sprintf("%+v", in), in.NativeSessionID)
	for _, bad := range []string{
		`null`, `{}`, good + ` {}`,
		strings.Replace(good, `"nativeSessionId":"signed-native-session"`, `"nativeSessionId":null`, 1),
		strings.Replace(good, `"nativeSessionId":"signed-native-session"`, `"nativeSessionId":"../foreign"`, 1),
		strings.Replace(good, `"nativeSessionId":"signed-native-session"`, `"nativeSessionId":""`, 1),
		strings.Replace(good, `"nativeSessionId"`, `"NativeSessionId"`, 1),
		strings.Replace(good, `"subject":"ordinary-staff"`, `"subject":"../other-staff"`, 1),
		strings.Replace(good, `019f39c2-1738-7998-b5de-6d4214f614e0`, `11111111-1111-4111-8111-111111111111`, 1),
		strings.TrimSuffix(good, "}") + `,"reviewed":true}`,
		strings.TrimSuffix(good, "}") + `,"target":{}}`,
		strings.TrimSuffix(good, "}") + `,"nativeSessionId":"other-session"}`,
	} {
		var value StaffMFAAdmissionInput
		require.Error(t, json.Unmarshal([]byte(bad), &value))
		require.Zero(t, value)
	}
}

func TestStaffMFAAdmissionReceiptExposesNoSessionOrStaffSource(t *testing.T) {
	var receipt StaffMFAAdmissionReceipt
	require.NoError(t, json.Unmarshal([]byte(`{"generationId":"019f39c2-1738-7998-b5de-6d4214f614e0"}`), &receipt))
	require.Equal(t, "019f39c2-1738-7998-b5de-6d4214f614e0", receipt.GenerationID)
	for _, bad := range []string{`null`, `{}`, `{"generationId":"11111111-1111-4111-8111-111111111111"}`, `{"generationId":"019f39c2-1738-7998-b5de-6d4214f614e0","nativeSessionId":"private"}`} {
		var value StaffMFAAdmissionReceipt
		require.Error(t, json.Unmarshal([]byte(bad), &value))
	}
}
