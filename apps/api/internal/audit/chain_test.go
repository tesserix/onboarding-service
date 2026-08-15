package audit

import "testing"

func event(action, prev string) Event {
	return Event{
		OccurredAt: fixedTime,
		ActorType:  ActorPlatform,
		ActorID:    "sam@tesserix.app",
		Action:     action,
		Outcome:    OutcomeSuccess,
		RequestID:  "req-1",
		PrevHash:   prev,
	}
}

func TestHashIsDeterministic(t *testing.T) {
	a, b := event("org.create", Genesis), event("org.create", Genesis)
	if a.Hash() != b.Hash() {
		t.Fatalf("same event hashed differently: %s vs %s", a.Hash(), b.Hash())
	}
}

func TestHashCoversEveryField(t *testing.T) {
	base := event("org.create", Genesis)
	mutations := map[string]func(*Event){
		"occurredAt": func(e *Event) { e.OccurredAt = e.OccurredAt.Add(1) },
		"actorType":  func(e *Event) { e.ActorType = ActorProduct },
		"actorID":    func(e *Event) { e.ActorID = "mallory" },
		"productKey": func(e *Event) { e.ProductKey = "hms" },
		"orgID":      func(e *Event) { e.OrgID = "9d1f" },
		"action":     func(e *Event) { e.Action = "org.delete" },
		"target":     func(e *Event) { e.Target = "org/1" },
		"outcome":    func(e *Event) { e.Outcome = OutcomeDenied },
		"requestID":  func(e *Event) { e.RequestID = "req-2" },
		"detail":     func(e *Event) { e.Detail = `{"k":1}` },
		"prevHash":   func(e *Event) { e.PrevHash = "deadbeef" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			altered := base
			mutate(&altered)
			if altered.Hash() == base.Hash() {
				t.Fatalf("changing %s did not change the hash", name)
			}
		})
	}
}

// Field values must not be able to impersonate a field boundary: an actor id of
// "a|b" must not hash the same as actor "a" with the next field starting "b".
func TestHashIsNotAmbiguousAcrossFieldBoundaries(t *testing.T) {
	a := event("org.create", Genesis)
	a.ActorID, a.ProductKey = "a", "b"
	b := event("org.create", Genesis)
	b.ActorID, b.ProductKey = "a\nb", ""
	if a.Hash() == b.Hash() {
		t.Fatal("field boundary is forgeable by embedding the separator")
	}
}

func TestVerifyAcceptsAnIntactChain(t *testing.T) {
	chain := build("org.create", "grant.create", "idp.create")
	if err := Verify(chain); err != nil {
		t.Fatalf("intact chain rejected: %v", err)
	}
}

func TestVerifyAcceptsAnEmptyChain(t *testing.T) {
	if err := Verify(nil); err != nil {
		t.Fatalf("empty chain rejected: %v", err)
	}
}

func TestVerifyRejectsAnEditedEvent(t *testing.T) {
	chain := build("org.create", "grant.create", "idp.create")
	chain[1].Outcome = OutcomeDenied // hash left as it was

	err := Verify(chain)
	if err == nil {
		t.Fatal("edited event accepted")
	}
	var broken *ChainError
	if !as(err, &broken) || broken.Index != 1 {
		t.Fatalf("want ChainError at index 1, got %v", err)
	}
}

func TestVerifyRejectsARemovedEvent(t *testing.T) {
	chain := build("org.create", "grant.create", "idp.create")
	shortened := []Record{chain[0], chain[2]}

	if err := Verify(shortened); err == nil {
		t.Fatal("chain with a removed event accepted")
	}
}

func TestVerifyRejectsAChainNotRootedInGenesis(t *testing.T) {
	chain := build("org.create", "grant.create")
	if err := Verify(chain[1:]); err == nil {
		t.Fatal("chain not rooted in genesis accepted")
	}
}

func TestVerifyRejectsAForgedTail(t *testing.T) {
	chain := build("org.create", "grant.create")
	forged := event("org.delete", chain[1].StoredHash)
	// A forger who recomputes the hash of their own appended event still
	// cannot do so without the whole prefix, but an intact append is legal —
	// what must fail is appending with someone else's prev_hash.
	forged.PrevHash = chain[0].StoredHash
	chain = append(chain, Record{Event: forged, StoredHash: forged.Hash()})

	if err := Verify(chain); err == nil {
		t.Fatal("event appended onto a stale prev_hash accepted")
	}
}
