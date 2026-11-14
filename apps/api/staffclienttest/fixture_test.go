package staffclienttest_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/staffclient"
	"github.com/tesserix/onboarding-service/api/staffclienttest"
	"github.com/tesserix/onboarding-service/api/staffcontract"
)

func TestRealOwnerFixtureCreatesOnlyOnceAndCompletesPrivateRecipientSetup(t *testing.T) {
	dsn, schema := os.Getenv("ONBOARDING_TEST_DSN"), os.Getenv("ONBOARDING_TEST_SCHEMA")
	if dsn == "" || schema == "" {
		t.Fatal("database integration requires canonical schema and isolated database")
	}
	tenant := uuid.New()
	f := staffclienttest.New(t, dsn, schema, tenant)
	scope := staffclient.Scope{TenantID: tenant, CommandID: uuid.Must(uuid.NewV7()), ActorSubject: "TEST-owner"}
	input := staffcontract.StaffIdentityCreateInput{ActorSubject: scope.ActorSubject, Email: "synthetic@example.invalid", GivenName: "Synthetic", FamilyName: "Staff", Reason: "Authorized fixture account"}
	invalid := input
	invalid.Reason = ""
	if _, err := f.Client.Create(t.Context(), scope, invalid); !errors.Is(err, staffclient.ErrInvalid) {
		t.Fatal("invalid source input must remain distinguishable from an immutable-intent conflict")
	}
	for range 2 {
		receipt, err := f.Client.Create(t.Context(), scope, input)
		if err != nil || receipt.CommandState != "created" || receipt.UserID == "" {
			t.Fatalf("create receipt unavailable: %v", err)
		}
	}
	if f.Creates.Load() != 1 {
		t.Fatal("creation replay reached native twice")
	}
	foreign := scope
	foreign.TenantID = uuid.New()
	if _, err := f.Client.Get(t.Context(), foreign); err == nil {
		t.Fatal("foreign tenant read creation")
	}
	foreign = scope
	foreign.ActorSubject = "different-owner"
	if _, err := f.Client.Get(t.Context(), foreign); err == nil {
		t.Fatal("another initiator read creation")
	}
	delivery := uuid.Must(uuid.NewV7())
	for range 2 {
		receipt, err := f.Client.Deliver(t.Context(), scope, delivery)
		if err != nil || receipt.State != "email_action_required" {
			t.Fatalf("invitation unavailable: %v", err)
		}
	}
	if f.Mails.Load() != 1 {
		t.Fatal("delivery replay repeated mail")
	}
	call := func(action string) string {
		in := f.Invitation()
		payload := map[string]string{"invitation_id": in.InvitationID.String(), "ticket": in.Ticket}
		if action == "email" || action == "password" {
			payload["code"] = in.Code
		}
		if action == "password" {
			payload["password"] = "private-fixture-password"
		}
		if action == "resend" {
			payload["delivery_id"] = uuid.Must(uuid.NewV7()).String()
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/internal/v1/customer-signups/staff-invitations/"+action, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+staffclienttest.WorkloadToken)
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		f.Router.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("recipient %s status: %d", action, response.Code)
		}
		if strings.Contains(response.Body.String(), in.Ticket) || strings.Contains(response.Body.String(), in.Code) {
			t.Fatal("private proof disclosed in receipt")
		}
		var receipt staffcontract.StaffInvitationReceipt
		if json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.Validate() != nil {
			t.Fatal("invalid recipient receipt")
		}
		return receipt.State
	}
	firstEmail := call("email")
	replayedEmail := call("email")
	if firstEmail != "already_verified" || replayedEmail != "already_verified" {
		t.Fatal("verification did not become redundant")
	}
	if call("resend") != "password_action_required" {
		t.Fatal("verified contact did not progress to private password setup")
	}
	firstPassword := call("password")
	replayedPassword := call("password")
	completedResend := call("resend")
	if firstPassword != "complete" || replayedPassword != "complete" || completedResend != "complete" {
		t.Fatal("password setup did not complete idempotently")
	}
	if f.Mails.Load() != 2 || f.Creates.Load() != 1 {
		t.Fatal("completion repeated external side effects")
	}
}
