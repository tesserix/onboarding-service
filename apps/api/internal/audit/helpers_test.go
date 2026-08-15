package audit

import (
	"errors"
	"time"
)

var fixedTime = time.Date(2026, 8, 16, 9, 0, 0, 0, time.UTC)

func build(actions ...string) []Record {
	chain := make([]Record, 0, len(actions))
	prev := Genesis
	for _, action := range actions {
		e := event(action, prev)
		h := e.Hash()
		chain = append(chain, Record{Event: e, StoredHash: h})
		prev = h
	}
	return chain
}

func as(err error, target **ChainError) bool { return errors.As(err, target) }
