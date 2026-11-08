// Status indicators: p2's accent/muted/warning colors (status.ts, not Pi 1.2.0's editor recoloring) and the retry countdown goroutine.
//
// pi: apps/p2/src/tui/components/status.ts
package components

import (
	"context"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pitui "github.com/MichaelKinsy/PiG/tui"
)

// goroutinesIn counts goroutines whose stack mentions fn.
func goroutinesIn(fn string) int {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	n := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, fn) {
			n++
		}
	}
	return n
}

// settle yields until cond holds; the goroutine under test needs scheduling, not a sleep, to react to cancellation.
func settle(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		runtime.Gosched()
	}
}

func TestStatusIndicatorsUseP2Colors(t *testing.T) {
	ctx := newTestContext(t)
	screen, _ := newScreen(t, 80, 24)
	th := ctx.Theme().Current()
	editor := NewCustomEditor(screen, ctx, pitui.EditorOptions{})
	for _, tc := range []struct {
		name      string
		indicator *StatusIndicator
		word      string
		message   string
	}{
		{"working", NewWorkingStatusIndicator(screen, ctx, "Working...", nil), "Working...", "muted"},
		{"compaction", NewCompactionStatusIndicator(screen, ctx, "manual", nil), "Compacting context... (ctrl+o to cancel)", "muted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer tc.indicator.Dispose()
			editor.SetWorkingStatusIndicator(tc.indicator)
			top := editor.Render(60)[0]
			// The editor's border color must not repaint the label: p2 colors the message muted itself.
			contains(t, top, th.Fg(tc.message, tc.word))
			if strings.Contains(top, th.Fg("borderMuted", tc.word)) {
				t.Fatalf("label recolored by the editor border: %q", top)
			}
		})
	}
}

func TestRetryStatusIndicatorCountsDown(t *testing.T) {
	ctx := newTestContext(t)
	screen, _ := newScreen(t, 80, 24)
	var clock atomic.Int64
	clock.Store(1_000)
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	indicator := newRetryStatusIndicator(t.Context(), screen, ctx, 1, 4_000, "overloaded", time.Millisecond, now)
	defer indicator.Dispose()
	if got := messageOf(indicator); !strings.Contains(got, "Retrying (attempt 2) in 3s: overloaded (ctrl+o to cancel)") {
		t.Fatalf("initial message %q", got)
	}
	clock.Store(2_500)
	settle(t, func() bool { return strings.Contains(messageOf(indicator), "in 2s") })
	clock.Store(4_000)
	// At the retry time the countdown ends by itself: no goroutine remains without Dispose.
	settle(t, func() bool {
		return strings.Contains(messageOf(indicator), "in 0s") && goroutinesIn("newRetryStatusIndicator") == 0
	})
}

func messageOf(s *StatusIndicator) string {
	// The ticker goroutine writes the message under the loader's lock; reading it back through a render is the safe way.
	return plain(s.Render(200))
}

func TestRetryStatusIndicatorDisposeStopsTimer(t *testing.T) {
	ctx := newTestContext(t)
	screen, _ := newScreen(t, 80, 24)
	before := goroutinesIn("newRetryStatusIndicator")
	indicator := NewRetryStatusIndicator(t.Context(), screen, ctx, 0, time.Now().Add(time.Hour).UnixMilli(), "boom")
	if got := goroutinesIn("newRetryStatusIndicator"); got != before+1 {
		t.Fatalf("timer goroutines = %d, want %d", got, before+1)
	}
	indicator.Dispose()
	// Dispose joins the goroutine: no waiting is needed.
	if got := goroutinesIn("newRetryStatusIndicator"); got != before {
		t.Fatalf("timer goroutine leaked after Dispose: %d", got)
	}
	indicator.Dispose() // idempotent
}

func TestRetryStatusIndicatorContextStopsTimer(t *testing.T) {
	ctx := newTestContext(t)
	screen, _ := newScreen(t, 80, 24)
	before := goroutinesIn("newRetryStatusIndicator")
	runCtx, cancel := context.WithCancel(t.Context())
	indicator := NewRetryStatusIndicator(runCtx, screen, ctx, 0, time.Now().Add(time.Hour).UnixMilli(), "boom")
	cancel()
	settle(t, func() bool { return goroutinesIn("newRetryStatusIndicator") == before })
	indicator.Dispose() // after the context ended, Dispose must not block
}
