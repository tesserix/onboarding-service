package auth

import "strings"

// Allowlist is the set of humans who may hold a console session. It is the
// backstop for an identity-provider misconfiguration: a valid Google sign-in
// from anyone else is still refused.
type Allowlist map[string]struct{}

func ParseAllowlist(raw string) Allowlist {
	list := Allowlist{}
	for _, entry := range strings.Split(raw, ",") {
		if email := strings.ToLower(strings.TrimSpace(entry)); email != "" {
			list[email] = struct{}{}
		}
	}
	return list
}

// Allows compares the address verbatim. Gmail's dot and +tag aliasing is
// deliberately not honoured: normalising it would widen the list to addresses
// nobody put on it.
func (a Allowlist) Allows(email string) bool {
	_, ok := a[strings.ToLower(strings.TrimSpace(email))]
	return ok
}
