// Package audit is the append-only, hash-chained ledger of every onboarding
// action. The chain is what makes the ledger tamper-evident: the database
// grants stop the application deleting a row, the chain proves nobody else did
// either. See tesserix-k8s docs/onboarding-api.md § 5.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Genesis is the prev_hash of the first event in the chain.
const Genesis = "genesis"

type ActorType string

const (
	ActorPlatform ActorType = "platform"
	ActorProduct  ActorType = "product"
	ActorOrgAdmin ActorType = "org_admin"
	ActorSystem   ActorType = "system"
)

type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeDenied  Outcome = "denied"
	OutcomeError   Outcome = "error"
)

type Event struct {
	OccurredAt time.Time `json:"occurredAt"`
	ActorType  ActorType `json:"actorType"`
	ActorID    string    `json:"actorId"`
	ProductKey string    `json:"productKey,omitempty"`
	OrgID      string    `json:"orgId,omitempty"`
	Action     string    `json:"action"`
	Target     string    `json:"target,omitempty"`
	Outcome    Outcome   `json:"outcome"`
	RequestID  string    `json:"requestId"`
	Detail     string    `json:"detail,omitempty"`
	PrevHash   string    `json:"-"`
}

// Record is an Event as read back from the ledger, carrying the hash that was
// stored with it rather than the one it hashes to now. Verify compares them.
type Record struct {
	Event
	Seq int64 `json:"seq"`
	// The stored hash stays internal: it is the ledger's own integrity record,
	// and Verify is what a reader should trust, not a hash on screen.
	StoredHash string `json:"-"`
}

// Hash is a SHA-256 over every field. Fields are length-prefixed rather than
// merely delimited so no field value can forge a boundary.
func (e Event) Hash() string {
	var b strings.Builder
	for _, field := range []string{
		e.OccurredAt.UTC().Format(time.RFC3339Nano),
		string(e.ActorType),
		e.ActorID,
		e.ProductKey,
		e.OrgID,
		e.Action,
		e.Target,
		string(e.Outcome),
		e.RequestID,
		e.Detail,
		e.PrevHash,
	} {
		fmt.Fprintf(&b, "%d:%s|", len(field), field)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}

type ChainError struct {
	Index  int
	Seq    int64
	Reason string
}

func (e *ChainError) Error() string {
	return fmt.Sprintf("audit: chain broken at index %d (seq %d): %s", e.Index, e.Seq, e.Reason)
}

// Verify walks a chain in ledger order and reports the first break.
func Verify(chain []Record) error {
	prev := Genesis
	for i, r := range chain {
		if r.PrevHash != prev {
			return &ChainError{Index: i, Seq: r.Seq, Reason: "prev_hash does not match the preceding event"}
		}
		if r.Hash() != r.StoredHash {
			return &ChainError{Index: i, Seq: r.Seq, Reason: "stored hash does not match the event"}
		}
		prev = r.StoredHash
	}
	return nil
}
