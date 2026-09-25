package tools

import (
	"errors"
	"path/filepath"
	"testing"
)

// withFileMutationQueue returns the callback rejection while its finally block
// releases the path for the next mutation.
func TestMutationQueuePreservesFailureAndReleasesPath(t *testing.T) {
	t.Parallel()
	queue := NewFileMutationQueue()
	path := filepath.Join(t.TempDir(), "pending")
	failure := errors.New("write refused")
	if err := queue.With(path, func() error { return failure }); !errors.Is(err, failure) {
		t.Fatalf("mutation error = %v", err)
	}
	called := false
	if err := queue.With(path, func() error { called = true; return nil }); err != nil || !called {
		t.Fatalf("next mutation called = %v, error = %v", called, err)
	}
}
