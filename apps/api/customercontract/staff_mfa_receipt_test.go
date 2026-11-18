package customercontract

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStaffMFARecoveryReceiptHasClosedMinimalState(t *testing.T) {
	for _, state := range []string{"prepared", "processing", "awaiting_enrollment", "unconfirmed", "cancelled", "completed"} {
		raw := `{"commandId":"019f39c2-1738-7998-b5de-6d4214f614e0","state":"` + state + `"}`
		var receipt StaffMFARecoveryReceipt
		require.NoError(t, json.Unmarshal([]byte(raw), &receipt))
		for _, bad := range []string{
			strings.Replace(raw, `"state":"`+state+`"`, `"state":"ready"`, 1),
			strings.Replace(raw, `"state":"`+state+`"`, `"state":null`, 1),
			strings.Replace(raw, `"commandId"`, `"CommandId"`, 1),
			strings.TrimSuffix(raw, "}") + `,"userId":"private-native-account"}`,
			strings.TrimSuffix(raw, "}") + `,"state":"completed"}`,
		} {
			var receipt StaffMFARecoveryReceipt
			require.Error(t, json.Unmarshal([]byte(bad), &receipt))
			require.Zero(t, receipt)
		}
	}
}
