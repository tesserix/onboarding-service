package customercontract

import (
	"encoding/hex"
	"github.com/google/uuid"
	"net/mail"
	"strings"
	"unicode"
)

// DiscoveryStart is accepted only from the Helivanta workload, before identity lookup.
type DiscoveryStart struct {
	ID          string `json:"id" format:"uuid" required:"true"`
	BrowserHash string `json:"browserHash" required:"true" pattern:"^[a-f0-9]{64}$"`
	SourceHash  string `json:"sourceHash" required:"true" pattern:"^[a-f0-9]{64}$"`
	Email       string `json:"email" format:"email" required:"true" max:"200" clean:"true"`
}

func (in DiscoveryStart) Validate() error {
	if (DiscoveryAccess{in.ID, in.BrowserHash}).Validate() != nil || !discoveryHash(in.SourceHash) || len(in.Email) > 200 || strings.TrimSpace(in.Email) != in.Email {
		return ErrInvalid
	}
	address, err := mail.ParseAddress(in.Email)
	if err != nil || address.Name != "" || address.Address != in.Email {
		return ErrInvalid
	}
	return nil
}

// DiscoveryAccess binds mailbox discovery to a random, host-only browser capability.
type DiscoveryAccess struct {
	ID          string `json:"id" format:"uuid" required:"true"`
	BrowserHash string `json:"browserHash" required:"true" pattern:"^[a-f0-9]{64}$"`
}

func (in DiscoveryAccess) Validate() error {
	id, err := uuid.Parse(in.ID)
	if err != nil || id.Version() != 7 || id.String() != in.ID || !discoveryHash(in.BrowserHash) {
		return ErrInvalid
	}
	return nil
}
func discoveryHash(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == value
}

// DiscoveryProof is a one-use mailbox code; it never authenticates a native account.
type DiscoveryProof struct {
	ID          string `json:"id" format:"uuid" required:"true"`
	BrowserHash string `json:"browserHash" required:"true" pattern:"^[a-f0-9]{64}$"`
	Code        string `json:"code" required:"true" pattern:"^[0-9]{8}$"`
}

func (in DiscoveryProof) Validate() error {
	if (DiscoveryAccess{in.ID, in.BrowserHash}).Validate() != nil || len(in.Code) != 8 {
		return ErrInvalid
	}
	for _, r := range in.Code {
		if r < '0' || r > '9' {
			return ErrInvalid
		}
	}
	return nil
}

// DiscoveryRequest is the public email-only submission; it reveals no matches.
type DiscoveryRequest struct {
	Email string `json:"email" format:"email" required:"true" max:"200" clean:"true"`
}

// DiscoveryCode is the public mailbox proof, with browser identity supplied by cookie.
type DiscoveryCode struct {
	Code string `json:"code" required:"true" pattern:"^[0-9]{8}$"`
}

// DiscoveryChoice contains display data and a routing hint, never credentials or authority.
type DiscoveryChoice struct {
	TenantID string `json:"tenantId" format:"uuid" required:"true"`
	Name     string `json:"name" required:"true" max:"200" clean:"true"`
	Account  string `json:"account" required:"true" max:"200" clean:"true"`
}

// DiscoveryChoices is disclosed only after mailbox proof; overflow fails closed.
type DiscoveryChoices struct {
	Accounts []DiscoveryChoice `json:"accounts" maxItems:"20"`
}

// DiscoveryAccepted is identical for all syntactically valid mailbox submissions.
type DiscoveryAccepted struct {
	State string `json:"state" enum:"check_email" required:"true"`
}

// DiscoveryVerified permits listing choices until the original challenge expiry.
type DiscoveryVerified struct {
	State string `json:"state" enum:"verified" required:"true"`
}

func (DiscoveryProof) String() string   { return "[redacted mailbox proof]" }
func (DiscoveryProof) GoString() string { return "[redacted mailbox proof]" }
func (DiscoveryCode) String() string    { return "[redacted mailbox proof]" }
func (DiscoveryCode) GoString() string  { return "[redacted mailbox proof]" }

func (in DiscoveryChoices) Validate() error {
	if in.Accounts == nil || len(in.Accounts) > 20 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, choice := range in.Accounts {
		if validateTaggedFields(choice, "") != nil || seen[choice.TenantID] || len(choice.Name) > 200 || strings.TrimSpace(choice.Name) != choice.Name || strings.ContainsFunc(choice.Name, unicode.IsControl) || (DiscoveryRequest{choice.Account}).Validate() != nil {
			return ErrInvalid
		}
		seen[choice.TenantID] = true
	}
	return nil
}
func (in DiscoveryRequest) Validate() error {
	address, err := mail.ParseAddress(in.Email)
	if err != nil || address.Name != "" || address.Address != in.Email || len(in.Email) > 200 || strings.TrimSpace(in.Email) != in.Email {
		return ErrInvalid
	}
	return nil
}
