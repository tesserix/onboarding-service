package customercontract

// LoginTargetsInput is workload-only; the caller holds an unscoped native auth
// request and discloses nothing from the answer before a password succeeds.
type LoginTargetsInput struct {
	Email     string `json:"email" required:"true" maxLength:"200"`
	RequestID string `json:"requestId" format:"uuid" required:"true"`
}

func (in LoginTargetsInput) Validate() error {
	if validateTaggedFields(in, "") != nil {
		return ErrInvalid
	}
	return validLoginEmail(in.Email)
}

// LoginCandidate is an exact native binding plus the display data the caller
// shows only after that binding's password check succeeded.
type LoginCandidate struct {
	TenantID       string `json:"tenantId" format:"uuid" required:"true"`
	OrganizationID string `json:"organizationId"`
	Subject        string `json:"subject" required:"true" pattern:"^[A-Za-z0-9_-]{1,200}$"`
	Name           string `json:"name" required:"true" max:"200" clean:"true"`
	Account        string `json:"account" required:"true" max:"200" clean:"true"`
}

// LoginTargets lists at most five candidates. Past five it lists none and sets
// Truncated: the caller refuses rather than checking an arbitrary subset.
type LoginTargets struct {
	Targets   []LoginCandidate `json:"targets" maxItems:"5"`
	Truncated bool             `json:"truncated"`
}

// MaxLoginTargets bounds the password checks one submission can cause.
const MaxLoginTargets = 5

func (in LoginTargets) Validate() error {
	if in.Targets == nil || len(in.Targets) > MaxLoginTargets || (in.Truncated && len(in.Targets) != 0) {
		return ErrInvalid
	}
	choices := make([]DiscoveryChoice, 0, len(in.Targets))
	for _, target := range in.Targets {
		if validateTaggedFields(target, "") != nil || (LoginTarget{target.TenantID, target.OrganizationID, target.Subject}).Validate() != nil {
			return ErrInvalid
		}
		choices = append(choices, DiscoveryChoice{TenantID: target.TenantID, Name: target.Name, Account: target.Account})
	}
	// Same display and uniqueness rules as discovery choices.
	return DiscoveryChoices{Accounts: choices}.Validate()
}
