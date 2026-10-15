package coding

import (
	"errors"
	"fmt"
	"testing"
)

// A session_before_compact cancellation is recognised by error identity, so a wrapped sentinel is still a cancellation.
func TestIsCompactionCancelledMatchesTheWrappedSentinel(t *testing.T) {
	if !isCompactionCancelled(fmt.Errorf("compact: %w", errCompactionCancelled)) {
		t.Fatal("a wrapped cancellation was not recognised")
	}
	if isCompactionCancelled(errors.New("boom")) || isCompactionCancelled(nil) {
		t.Fatal("another error was taken for a cancellation")
	}
}
