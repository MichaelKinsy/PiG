package data_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/chord/next/data"
)

// Pi packages/chord/src/next/data/errors.ts:50 `this.name = "ChordError"`; D103: the inherited name is the error type's Name().
func TestChordErrorNameIsChordError(t *testing.T) {
	var err error = data.Invalid(data.ReasonArgs, "bad")
	named, ok := err.(interface{ Name() string })
	if !ok {
		t.Fatal("ChordError has no Name() string member")
	}
	if got := named.Name(); got != "ChordError" {
		t.Errorf("Name() = %q, want %q", got, "ChordError")
	}
}
