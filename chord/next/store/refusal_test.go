package store

import (
	"errors"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Ports packages/chord/test/next/store/refusal.test.ts: tests of refusals as Chord's owners take them (slice 6b D4,
// §1.4): an `int` CHECK fails only its transaction, any other refusal its object; the store's own errors pass
// through.
//
// Pending: the "Chord's owners" case ("an int out of range fails only its transaction; the object stays live") runs a
// Chord host and a local object (`loaded`, `host.serve`, `self.tx`), which is outside this package; it is ported with
// the host layer.

// codedError is an error with SQLite's extended result code, as Node's binding reports `errcode`.
type codedError struct {
	message string
	code    int
}

func (err *codedError) Error() string { return err.message }
func (err *codedError) Code() int     { return err.code }

func TestClassification(t *testing.T) {
	t.Run("a CHECK failure by SQLite's extended code or by its message; everything else broken; the store's own errors pass", func(t *testing.T) {
		node := &codedError{message: "CHECK constraint failed: n", code: 275}
		other := &codedError{message: "UNIQUE constraint failed", code: 2067}
		check := RefusalOf(node, "x")
		if check == nil || check.Kind != RefusalCheck || check.Error.Code != data.CodeInvalid || check.Error.Reason != data.ReasonArgs {
			t.Fatalf("RefusalOf(node) = %+v, want check invalid/args", check)
		}
		if refusal := RefusalOf(errors.New("CHECK constraint failed: n"), "x"); refusal == nil || refusal.Kind != RefusalCheck {
			t.Fatalf("RefusalOf(by message) = %+v, want check", refusal)
		}
		broken := RefusalOf(other, "x")
		if broken == nil || broken.Kind != RefusalBroken || broken.Error.Code != data.CodeUnavailable {
			t.Fatalf("RefusalOf(other) = %+v, want broken unavailable", broken)
		}
		stopped := data.NewChordError(data.ChordErrorInit{Code: data.CodeUnavailable, Reason: data.ReasonStorageFailed, Message: "store: storage failed"}, nil)
		if refusal := RefusalOf(stopped, "x"); refusal != nil {
			t.Fatalf("RefusalOf(stopped) = %+v, want nil", refusal)
		}
		released := data.NewChordError(data.ChordErrorInit{Code: data.CodeReleased, Message: "closed"}, nil)
		if refusal := RefusalOf(released, "x"); refusal != nil {
			t.Fatalf("RefusalOf(released) = %+v, want nil", refusal)
		}
	})
}
