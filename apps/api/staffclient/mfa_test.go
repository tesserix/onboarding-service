package staffclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/tesserix/onboarding-service/api/customercontract"
)

func mfaRequest(t *testing.T) (Scope, customercontract.StaffMFARecoveryRequest) {
	t.Helper()
	s := Scope{TenantID: uuid.New(), CommandID: uuid.Must(uuid.NewV7()), ActorSubject: "tenant-admin"}
	link := uuid.Must(uuid.NewV7()).String()
	in := customercontract.StaffMFARecoveryRequest{
		ActorSubject: s.ActorSubject,
		Target: customercontract.StaffMFARecoveryTarget{
			TenantID: s.TenantID.String(), EmployerKind: "hospital", EmployerID: uuid.NewString(),
			StaffID: uuid.NewString(), StaffVersion: 7, LinkID: link, LinkVersion: 3, Subject: "ordinary-staff",
		},
		Intent: customercontract.StaffMFAResetInput{CommandID: s.CommandID.String(), ExpectedLinkID: link,
			ExpectedLinkVersion: 3, ExpectedStaffVersion: 7, Reason: "Lost authenticator reported in person"},
	}
	if err := in.Validate(); err != nil {
		t.Fatal(err)
	}
	return s, in
}

func TestMFAWorkflowRoutesAndReadsExactSnapshot(t *testing.T) {
	s, in := mfaRequest(t)
	for _, operation := range []string{"dispatch", "cancel", "read"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			c := mfaClient(t, func(req *http.Request) (*http.Response, error) {
				calls++
				base := "/v1/orgs/" + s.TenantID.String() + "/staff-mfa-recoveries/" + s.CommandID.String()
				state := "cancelled"
				if operation == "read" {
					if req.Method != "GET" || req.URL.Path != base || req.URL.RawQuery != "actor_subject=tenant-admin" || req.ContentLength != 0 {
						t.Fatal("wrong read route")
					}
					raw, err := json.Marshal(customercontract.StaffMFARecoverySnapshot{Request: in, Receipt: customercontract.StaffMFARecoveryReceipt{CommandID: s.CommandID.String(), State: "processing"}})
					if err != nil {
						t.Fatal(err)
					}
					return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
				}
				if req.Method != "POST" || req.URL.Path != base+"/"+operation || req.URL.RawQuery != "" {
					t.Fatal("wrong mutation route")
				}
				if operation == "dispatch" {
					var got customercontract.StaffMFADispatchRequest
					if json.NewDecoder(req.Body).Decode(&got) != nil || got.Request != in || got.ApplicationFenceID != s.CommandID.String() {
						t.Fatal("wrong application fence")
					}
					state = "unconfirmed"
				} else {
					var got customercontract.StaffMFARecoveryRequest
					if json.NewDecoder(req.Body).Decode(&got) != nil || got != in {
						t.Fatal("cancel intent changed")
					}
				}
				return &http.Response{StatusCode: 202, Body: io.NopCloser(strings.NewReader(`{"commandId":"` + s.CommandID.String() + `","state":"` + state + `"}`))}, nil
			})
			switch operation {
			case "dispatch":
				out, err := c.DispatchMFA(t.Context(), s, customercontract.StaffMFADispatchRequest{Request: in, ApplicationFenceID: s.CommandID.String()})
				if err != nil || out.State != "unconfirmed" {
					t.Fatalf("dispatch: %v", err)
				}
			case "cancel":
				out, err := c.CancelMFA(t.Context(), s, in)
				if err != nil || out.State != "cancelled" {
					t.Fatalf("cancel: %v", err)
				}
			case "read":
				out, err := c.GetMFA(t.Context(), s)
				if err != nil || out.Request != in || out.Receipt.State != "processing" {
					t.Fatalf("read: %v", err)
				}
			}
			if calls != 1 {
				t.Fatal("unexpected owner retry")
			}
		})
	}
}

func TestMFARejectsChangedAuthorityBeforeHTTP(t *testing.T) {
	s, in := mfaRequest(t)
	c := mfaClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid authority reached owner")
		return nil, nil
	})
	for _, mutate := range []func(*customercontract.StaffMFARecoveryRequest){
		func(r *customercontract.StaffMFARecoveryRequest) { r.ActorSubject = "other-admin" },
		func(r *customercontract.StaffMFARecoveryRequest) { r.Target.TenantID = uuid.NewString() },
		func(r *customercontract.StaffMFARecoveryRequest) {
			r.Intent.CommandID = uuid.Must(uuid.NewV7()).String()
		},
		func(r *customercontract.StaffMFARecoveryRequest) { r.Target.LinkVersion++ },
	} {
		bad := in
		mutate(&bad)
		if _, err := c.PrepareMFA(t.Context(), s, bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("changed prepare authority accepted")
		}
		if _, err := c.CancelMFA(t.Context(), s, bad); !errors.Is(err, ErrInvalid) {
			t.Fatal("changed cancel authority accepted")
		}
		if _, err := c.DispatchMFA(t.Context(), s, customercontract.StaffMFADispatchRequest{Request: bad, ApplicationFenceID: s.CommandID.String()}); !errors.Is(err, ErrInvalid) {
			t.Fatal("changed dispatch authority accepted")
		}
	}
	if _, err := c.DispatchMFA(t.Context(), s, customercontract.StaffMFADispatchRequest{Request: in, ApplicationFenceID: uuid.Must(uuid.NewV7()).String()}); !errors.Is(err, ErrInvalid) {
		t.Fatal("wrong fence accepted")
	}
	c.product = "other-product"
	if _, err := c.PrepareMFA(t.Context(), s, in); !errors.Is(err, ErrInvalid) {
		t.Fatal("other product accepted")
	}
	if _, err := c.GetMFA(t.Context(), s); !errors.Is(err, ErrInvalid) {
		t.Fatal("other product read accepted")
	}
}

func mfaClient(t *testing.T, transport responseTransport) *Client {
	t.Helper()
	c, err := New(Config{Endpoint: "http://onboarding-api.onboarding.svc.cluster.local:8080", ProductKey: "helivanta",
		Tokens: tokenSource(func(context.Context) (string, error) { return "test-workload-token", nil }), HTTP: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPrepareMFABindsCanonicalRequestAndReceipt(t *testing.T) {
	s, in := mfaRequest(t)
	calls := 0
	c := mfaClient(t, func(req *http.Request) (*http.Response, error) {
		calls++
		if req.Method != http.MethodPut || req.URL.Path != "/v1/orgs/"+s.TenantID.String()+"/staff-mfa-recoveries/"+s.CommandID.String() || req.URL.RawQuery != "" {
			t.Fatal("wrong command route")
		}
		if req.Header.Get("Authorization") != "Bearer test-workload-token" || req.Header.Get("X-Expected-Product-Key") != "helivanta" || req.Header.Get("Cookie") != "" {
			t.Fatal("lost workload binding")
		}
		var got customercontract.StaffMFARecoveryRequest
		if json.NewDecoder(req.Body).Decode(&got) != nil || got != in {
			t.Fatal("request changed")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"commandId":"` + s.CommandID.String() + `","state":"prepared"}`))}, nil
	})
	out, err := c.PrepareMFA(t.Context(), s, in)
	if err != nil || out.CommandID != s.CommandID.String() || out.State != "prepared" || calls != 1 {
		t.Fatalf("invalid prepare result: %v", err)
	}
}

func TestMFARejectsUnverifiedResponsesWithoutRetry(t *testing.T) {
	s, in := mfaRequest(t)
	good := `{"commandId":"` + s.CommandID.String() + `","state":"processing"}`
	for _, tc := range []struct {
		name, body     string
		status         int
		transportError error
		want           error
	}{
		{"foreign command", strings.Replace(good, s.CommandID.String(), uuid.Must(uuid.NewV7()).String(), 1), 200, nil, ErrProtocol},
		{"duplicate state", strings.TrimSuffix(good, "}") + `,"state":"completed"}`, 200, nil, ErrProtocol},
		{"recased", strings.Replace(good, "commandId", "CommandId", 1), 200, nil, ErrProtocol},
		{"unknown", strings.TrimSuffix(good, "}") + `,"nativeToken":"private"}`, 200, nil, ErrProtocol},
		{"null", `null`, 200, nil, ErrProtocol},
		{"trailing", good + ` {}`, 200, nil, ErrProtocol},
		{"oversized", good + strings.Repeat(" ", 32769), 200, nil, ErrProtocol},
		{"unknown state", strings.Replace(good, "processing", "success", 1), 200, nil, ErrProtocol},
		{"redirect", "private", 302, nil, ErrUnavailable},
		{"forbidden", "private", 403, nil, ErrUnavailable},
		{"missing", "private", 404, nil, ErrNotFound},
		{"changed", "private", 409, nil, ErrConflict},
		{"dependency", "private", 503, nil, ErrUnavailable},
		{"lost acknowledgement", "", 0, errors.New("private transport failure"), ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := mfaClient(t, func(*http.Request) (*http.Response, error) {
				calls++
				if tc.transportError != nil {
					return nil, tc.transportError
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			out, err := c.DispatchMFA(t.Context(), s, customercontract.StaffMFADispatchRequest{Request: in, ApplicationFenceID: s.CommandID.String()})
			if !errors.Is(err, tc.want) || out != (customercontract.StaffMFARecoveryReceipt{}) || calls != 1 {
				t.Fatalf("unverified dispatch accepted/retried: %v (%d calls)", err, calls)
			}
		})
	}
}

func TestMFAReadRejectsForeignSnapshot(t *testing.T) {
	s, in := mfaRequest(t)
	for _, mutate := range []func(*customercontract.StaffMFARecoveryRequest){
		func(r *customercontract.StaffMFARecoveryRequest) { r.ActorSubject = "foreign-admin" },
		func(r *customercontract.StaffMFARecoveryRequest) { r.Target.TenantID = uuid.NewString() },
		func(r *customercontract.StaffMFARecoveryRequest) {
			r.Intent.CommandID = uuid.Must(uuid.NewV7()).String()
		},
	} {
		bad := in
		mutate(&bad)
		raw, err := json.Marshal(customercontract.StaffMFARecoverySnapshot{Request: bad, Receipt: customercontract.StaffMFARecoveryReceipt{CommandID: bad.Intent.CommandID, State: "prepared"}})
		if err != nil {
			t.Fatal(err)
		}
		c := mfaClient(t, func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(raw)))}, nil
		})
		out, err := c.GetMFA(t.Context(), s)
		if !errors.Is(err, ErrProtocol) || out != (customercontract.StaffMFARecoverySnapshot{}) {
			t.Fatal("foreign snapshot disclosed")
		}
	}
}
