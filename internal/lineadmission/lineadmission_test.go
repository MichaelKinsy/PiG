package lineadmission

import (
	"context"
	"testing"
)

func TestFromRunsTheCallbackOnce(t *testing.T) {
	if From(context.Background()) != nil {
		t.Fatal("a context without a callback returned one")
	}
	calls := 0
	ctx := With(context.Background(), func() { calls++ })
	derived, cancel := context.WithCancel(ctx)
	defer cancel()
	From(derived)()
	From(ctx)()
	if calls != 1 {
		t.Fatalf("callback ran %d times, want 1", calls)
	}
}
