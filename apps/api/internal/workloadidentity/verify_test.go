package workloadidentity_test

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/tesserix/onboarding-service/api/internal/workloadidentity"
	"k8s.io/client-go/rest"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkloadAudienceAndExactAccountAreBothRequired(t *testing.T) {
	for _, tc := range []struct {
		name, user, aud string
		authenticated   bool
		want            bool
	}{{"approved", "system:serviceaccount:onboarding:onboarding-api", "helivanta-provisioning", true, true}, {"foreign", "system:serviceaccount:foreign:onboarding-api", "helivanta-provisioning", true, false}, {"wrong audience", "system:serviceaccount:onboarding:onboarding-api", "other", true, false}, {"revoked", "system:serviceaccount:onboarding:onboarding-api", "helivanta-provisioning", false, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var v struct {
					Spec struct {
						Token     string   `json:"token"`
						Audiences []string `json:"audiences"`
					} `json:"spec"`
				}
				if e := json.NewDecoder(r.Body).Decode(&v); e != nil {
					t.Fatal(e)
				}
				if v.Spec.Token != "projected" || len(v.Spec.Audiences) != 1 || v.Spec.Audiences[0] != "helivanta-provisioning" {
					t.Error("unscoped review")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"apiVersion": "authentication.k8s.io/v1", "kind": "TokenReview", "status": map[string]any{"authenticated": tc.authenticated, "audiences": []string{tc.aud}, "user": map[string]any{"username": tc.user, "uid": "uid"}}})
			}))
			defer s.Close()
			cfg := &rest.Config{Host: s.URL, TLSClientConfig: rest.TLSClientConfig{CAData: pemCertificate(s.Certificate().Raw)}}
			verifier, e := workloadidentity.New(cfg, "helivanta-provisioning", "system:serviceaccount:onboarding:onboarding-api")
			if e != nil {
				t.Fatal(e)
			}
			e = verifier.Verify(context.Background(), "Bearer projected")
			if (e == nil) != tc.want {
				t.Fatalf("decision=%v want permitted=%v", e, tc.want)
			}
		})
	}
}

func pemCertificate(der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
