package auth

import "testing"

func TestAllowlistMatchesRegardlessOfCaseAndSpacing(t *testing.T) {
	list := ParseAllowlist(" first@example.test , Second@Example.test,third@example.test ")

	for _, email := range []string{
		"first@example.test",
		"FIRST@EXAMPLE.TEST",
		"second@example.test",
		"third@example.test",
	} {
		if !list.Allows(email) {
			t.Errorf("%q is on the list but was refused", email)
		}
	}
}

// Gmail treats dots and +tags as the same mailbox; the allowlist must not, or
// first+anything@example.test becomes a way in for anyone who can register it
// at another provider.
func TestAllowlistRefusesEveryoneElse(t *testing.T) {
	list := ParseAllowlist("first@example.test")

	for _, email := range []string{
		"",
		"firstt@example.test",
		"first+admin@example.test",
		"first@example.test.attacker.test",
		"attacker@example.test",
		"f.irst@example.test",
	} {
		if list.Allows(email) {
			t.Errorf("%q is not on the list but was allowed in", email)
		}
	}
}

// An empty allowlist must lock everyone out rather than let everyone in: it
// means the configuration failed to load, not that access is unrestricted.
func TestEmptyAllowlistAllowsNobody(t *testing.T) {
	for name, raw := range map[string]string{"empty": "", "spaces": "   ", "commas": ",,,"} {
		t.Run(name, func(t *testing.T) {
			list := ParseAllowlist(raw)
			if len(list) != 0 {
				t.Fatalf("parsed %d entries from %q", len(list), raw)
			}
			if list.Allows("first@example.test") {
				t.Fatal("an empty allowlist admitted a user")
			}
		})
	}
}
