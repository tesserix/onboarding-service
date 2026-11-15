package onboarding

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
)

func TestAccountChoiceFailureDiagnosticsExcludeMailboxProof(t *testing.T) {
	var output bytes.Buffer
	service := New(nil, nil, nil, slog.New(slog.NewJSONHandler(&output, nil)))
	proof := customercontract.DiscoveryAccess{ID: uuid.Must(uuid.NewV7()).String(), BrowserHash: strings.Repeat("b", 64)}
	_, err := service.AccountChoices(t.Context(), proof)
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing dependencies must remain unavailable")
	}
	if !strings.Contains(output.String(), `"phase":"dependencies"`) {
		t.Fatal("unavailable account choices did not report a fixed phase")
	}
	service.recordAccountLookupFailure(t.Context(), "scope_current_identity", time.Now(), errors.Join(ErrUnavailable, errors.New("PRIVATE-UPSTREAM-SECRET "+proof.BrowserHash)))
	for _, sensitive := range []string{proof.ID, proof.BrowserHash, "PRIVATE-UPSTREAM-SECRET"} {
		if strings.Contains(output.String(), sensitive) {
			t.Fatal("account lookup diagnostics disclosed mailbox proof")
		}
	}
}
