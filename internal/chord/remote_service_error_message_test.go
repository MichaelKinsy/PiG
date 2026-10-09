package chord_test

import (
	"testing"

	"github.com/MichaelKinsy/PiG/internal/chord"
)

// TestRemoteServiceErrorMessageIsTheConstructorMessage mirrors packages/chord/src/services/errors.ts:21-24: the constructor passes its message to Error, so message is what Error() reports.
// mutation-checked: a constructor that drops the message, and an Error() that differs from Message, fail it.
func TestRemoteServiceErrorMessageIsTheConstructorMessage(t *testing.T) {
	err := chord.NewRemoteServiceError(chord.ErrServiceStaleInstance, "stale")
	if err.Message != "stale" || err.Error() != "stale" || err.Code != chord.ErrServiceStaleInstance {
		t.Fatalf("error = %#v, want message and Error() \"stale\" with code %q", err, chord.ErrServiceStaleInstance)
	}
}
