package staffcontract_test

import (
	"strings"
	"testing"

	"github.com/tesserix/onboarding-service/api/staffcontract"
)

func TestStaffContactDecoderAcceptsOnlyCanonicalContactFields(t *testing.T) {
	raw := `{"email":"synthetic@example.invalid","given_name":"Synthetic","family_name":"Staff","reason":"Authorized account setup"}`
	in, err := staffcontract.DecodeStaffContact([]byte(raw))
	if err != nil || in.Email != "synthetic@example.invalid" || in.GivenName != "Synthetic" || in.FamilyName != "Staff" || in.Reason != "Authorized account setup" || in.ActorSubject != "" {
		t.Fatal("canonical contact fields were not decoded without adopting authority")
	}
	for _, body := range []string{
		strings.Replace(raw, `"email":`, `"Email":`, 1),
		strings.Replace(raw, `"email":`, `"email":"other@example.invalid","email":`, 1),
		strings.Replace(raw, `{`, `{"actor_subject":"forged",`, 1),
		strings.Replace(raw, `{`, `{"tenant_id":"forged",`, 1),
		strings.Replace(raw, `"Synthetic"`, `null`, 1),
		strings.Replace(raw, `"Synthetic"`, `42`, 1),
		strings.Replace(raw, `"Synthetic"`, `""`, 1),
		raw + `{}`, strings.Repeat(" ", 4097) + raw,
	} {
		if _, err := staffcontract.DecodeStaffContact([]byte(body)); err == nil {
			t.Fatal("ambiguous or authority-bearing contact body accepted")
		}
	}
}
