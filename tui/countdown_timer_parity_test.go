package tui

// countdown_timer_parity_test.go: upstream-parity transliteration of
// countdown-timer.ts (.upstream/current/packages/coding-agent/src/modes/
// interactive/components/countdown-timer.ts, 38 LOC).
//
// Why this is a parity artifact: the CountdownTimer surface lives inside
// dialog components (extension-input, login, oauth) that are not reachable
// hermetically via the faux provider: no faux stimulus can drive a tmux
// scenario to the dialog states that show the timer. AGENTS.md ("Authorized
// Option C / countdown-timer") explicitly permits a Go-side parity test as
// honest coverage when it pins specific observable strings/values from the
// upstream spec, paired with a boot-only scenario that proves the package
// links and runs (parity/scenarios/interactive-rendering/09-countdown-timer.toml).
//
// The four contracts asserted below mirror upstream's behavior exactly:
//
//   1. remainingSeconds initialization := Math.ceil(timeoutMs / 1000)
//   2. onTick fires immediately with the initial value, then once per second
//      with the decremented value
//   3. onExpire fires when remainingSeconds <= 0
//   4. dispose() halts further ticks (no callbacks after dispose)

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestCountdownTimerParity_InitialTickMatchesCeil locks contract (1):
// the initial onTick value must equal Math.ceil(timeoutMs / 1000),
// matching upstream countdown-timer.ts:21
// (`this.remainingSeconds = Math.ceil(timeoutMs / 1000);`).
func TestCountdownTimerParity_InitialTickMatchesCeil(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		want    int
	}{
		{"1ms ceils to 1s", 1 * time.Millisecond, 1},
		{"999ms ceils to 1s", 999 * time.Millisecond, 1},
		{"1000ms is 1s", 1000 * time.Millisecond, 1},
		{"1001ms ceils to 2s", 1001 * time.Millisecond, 2},
		{"5500ms ceils to 6s", 5500 * time.Millisecond, 6},
		{"30000ms is 30s", 30 * time.Second, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got int32 = -1
			ct := NewCountdownTimer(tc.timeout, func(s int) {
				atomic.CompareAndSwapInt32(&got, -1, int32(s))
			}, func() {})
			defer ct.Dispose()
			if g := atomic.LoadInt32(&got); int(g) != tc.want {
				t.Fatalf("initial onTick = %d, want %d (upstream Math.ceil(%dms/1000))",
					g, tc.want, tc.timeout/time.Millisecond)
			}
		})
	}
}

// TestCountdownTimerParity_TickDecrements locks contract (2): after the
// initial tick, onTick fires once per second with the decremented value.
// Upstream countdown-timer.ts:24-28:
//
//	this.intervalId = setInterval(() => {
//	    this.remainingSeconds--;
//	    this.onTick(this.remainingSeconds);
//	    ...
//	}, 1000);
//
// We assert the SEQUENCE of values observed within ~2.5s of a 3s timer:
// [3, 2, 1] (initial, then two interval ticks).
func TestCountdownTimerParity_TickDecrements(t *testing.T) {
	var mu sync.Mutex
	var ticks []int
	ct := NewCountdownTimer(3*time.Second, func(s int) {
		mu.Lock()
		ticks = append(ticks, s)
		mu.Unlock()
	}, func() {})
	defer ct.Dispose()

	// Wait long enough to observe initial tick + two interval ticks.
	time.Sleep(2500 * time.Millisecond)

	mu.Lock()
	got := append([]int(nil), ticks...)
	mu.Unlock()
	if len(got) < 3 {
		t.Fatalf("observed only %d ticks in 2.5s, want >=3: %v", len(got), got)
	}
	if got[0] != 3 || got[1] != 2 || got[2] != 1 {
		t.Fatalf("tick sequence = %v, want first three [3 2 1] (matches upstream decrement-then-emit)", got[:3])
	}
}

// TestCountdownTimerParity_OnExpireFiresAtZeroOrBelow locks contract (3):
// onExpire fires when remainingSeconds <= 0. Upstream countdown-timer.ts:29-32:
//
//	if (this.remainingSeconds <= 0) {
//	    this.dispose();
//	    this.onExpire();
//	}
func TestCountdownTimerParity_OnExpireFiresAtZeroOrBelow(t *testing.T) {
	expired := make(chan struct{}, 1)
	ct := NewCountdownTimer(1*time.Second, func(int) {}, func() {
		expired <- struct{}{}
	})
	defer ct.Dispose()

	select {
	case <-expired:
		// good
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("onExpire did not fire within 2.5s for a 1s timer")
	}
}

// TestCountdownTimerParity_DisposeHaltsTicks locks contract (4): after
// dispose(), no further callbacks fire. Upstream countdown-timer.ts:35-38:
//
//	dispose(): void {
//	    if (this.intervalId) {
//	        clearInterval(this.intervalId);
//	        this.intervalId = undefined;
//	    }
//	}
func TestCountdownTimerParity_DisposeHaltsTicks(t *testing.T) {
	var ticks atomic.Int32
	var expires atomic.Int32
	ct := NewCountdownTimer(5*time.Second,
		func(int) { ticks.Add(1) },
		func() { expires.Add(1) },
	)
	// Initial tick fires synchronously inside the constructor.
	if got := ticks.Load(); got != 1 {
		t.Fatalf("pre-dispose ticks = %d, want 1 (initial only)", got)
	}
	ct.Dispose()
	time.Sleep(1500 * time.Millisecond) // would have ticked once if still alive
	if got := ticks.Load(); got != 1 {
		t.Fatalf("post-dispose ticks = %d, want 1 (dispose must halt further ticks)", got)
	}
	if got := expires.Load(); got != 0 {
		t.Fatalf("post-dispose expires = %d, want 0 (dispose must halt expiration)", got)
	}
	// Double-dispose must be a no-op (matches upstream's `if (intervalId)` guard).
	ct.Dispose()
}
