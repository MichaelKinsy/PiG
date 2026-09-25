//go:build parity

package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitForHTReadyUsesSharedUpstreamFooterMatcher(t *testing.T) {
	calls := 0
	view := func() (string, error) {
		calls++
		if calls == 1 {
			return "pi starting", nil
		}
		return "~/repo\n0.0%/0 (auto) unknown\n", nil
	}
	got, err := waitForHTReady(context.Background(), "$0.000", time.Second, view)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !paneMatchesReadyPattern(got, "$0.000") {
		t.Fatalf("calls=%d output=%q", calls, got)
	}
}

func TestWaitForHTReadyHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitForHTReady(ctx, "Ready", time.Second, func() (string, error) { return "", nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
