package handleryield

import (
	"context"
	"testing"
)

func TestReportCallsTheYieldTheContextCarries(t *testing.T) {
	Report(context.Background()) // no yield: nothing to call
	calls := 0
	ctx := With(context.Background(), func() { calls++ })
	derived, cancel := context.WithCancel(ctx)
	defer cancel()
	Report(derived)
	Report(derived)
	if calls != 2 {
		t.Fatalf("yield calls = %d, want 2", calls)
	}
}
