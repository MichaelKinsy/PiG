package store

import (
	"errors"
	"strings"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/src/next/store/refusal.ts
//
// A refused commit as Chord's owners take it (slice 6b D4, §1.4): the store rejects a step it rolled back with the
// step's own error, unchanged (Durable's Storage semantics); Chord classifies it: an `int` column's `CHECK` (a
// plugin's value out of range) fails only its transaction, any other refusal means memory and disk disagree.

// RefusalKind is how a Chord commit ended that the store rejected: check fails the transaction only, broken its
// object too.
type RefusalKind string

// The refusal kinds.
const (
	RefusalCheck  RefusalKind = "check"
	RefusalBroken RefusalKind = "broken"
)

// Refusal is a classified store rejection.
type Refusal struct {
	Kind  RefusalKind
	Error *data.ChordError
}

// sqliteConstraintCheck is SQLite's extended result code SQLITE_CONSTRAINT_CHECK.
const sqliteConstraintCheck = 275

// RefusalOf is the refusal behind err, a store commit's rejection; nil when it is the store's own (`released` at
// admission or after close, `unavailable/storage_failed` once stopped), which the owner rethrows as it is.
func RefusalOf(err error, label string) *Refusal {
	if _, released := data.IsChordErrorCode(err, data.CodeReleased); released {
		return nil
	}
	if chordErr, ok := data.IsChordErrorCode(err, data.CodeUnavailable); ok && chordErr.Reason == data.ReasonStorageFailed {
		return nil
	}
	message := err.Error()
	if isCheck(err, message) {
		refused := label + ": a value is out of its column's range (" + message + ")"
		return &Refusal{Kind: RefusalCheck, Error: data.NewChordError(data.ChordErrorInit{Code: data.CodeInvalid, Reason: data.ReasonArgs, Message: refused}, err)}
	}
	refused := label + ": the store refused the commit (" + message + ")"
	return &Refusal{Kind: RefusalBroken, Error: data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Message: refused}, err)}
}

// isCheck reports whether a refusal is a CHECK failure: SQLite's extended code where the driver has one (an error
// with `Code() int`: modernc's carries 275 for a CHECK, proved by TestCheckFailureCode), the message otherwise (the
// Durable Object facade has no extended codes), as Pi's `typeof errcode === "number"` branch does: an error that has
// a code is told by it alone.
func isCheck(err error, message string) bool {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		return coded.Code() == sqliteConstraintCheck
	}
	return strings.Contains(message, "CHECK constraint failed")
}
