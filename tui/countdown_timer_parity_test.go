package tui

// pi: packages/coding-agent/src/modes/interactive/components/countdown-timer.ts

// countdown_timer_parity_test.go: upstream-parity transliteration of
// countdown-timer.ts (.upstream/current/packages/coding-agent/src/modes/
// interactive/components/countdown-timer.ts, 38 LOC). The dialogs that show
// the countdown are compared with Pi's components in
// TestExtensionDialogCountdownMatchesPi and end to end in
// test/parity/scenarios/interactive-rendering/09-countdown-timer.toml.
//
// The four contracts asserted below mirror upstream's behavior exactly:
//
//   1. remainingSeconds initialization := Math.ceil(timeoutMs / 1000)
//   2. onTick fires immediately with the initial value, then once per second
//      with the decremented value
//   3. onExpire fires when remainingSeconds <= 0
//   4. dispose() halts further ticks (no callbacks after dispose)

import (
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// TestCountdownTimerParity_InitialTickMatchesCeil locks contract (1):
// the initial onTick value must equal Math.ceil(timeoutMs / 1000),
// matching upstream countdown-timer.ts:21
// (`this.remainingSeconds = Math.ceil(timeoutMs / 1000);`).
func TestCountdownTimerParity_InitialTickMatchesCeil(t *testing.T) {
	t.Parallel()
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
			ct := NewCountdownTimer(tc.timeout, nil, func(s int) {
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
// We assert every tick and its boundary using synctest's clock, without waiting for wall time.
func TestCountdownTimerParity_TickDecrements(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var ticks []int
		ct := NewCountdownTimer(3*time.Second, nil, func(s int) {
			mu.Lock()
			ticks = append(ticks, s)
			mu.Unlock()
		}, func() {})
		defer ct.Dispose()

		synctest.Wait() // The timer goroutine has installed its ticker.
		for _, want := range [][]int{{3}, {3, 2}, {3, 2, 1}} {
			synctest.Wait()
			mu.Lock()
			got := append([]int(nil), ticks...)
			mu.Unlock()
			if !slices.Equal(got, want) {
				t.Fatalf("tick sequence = %v, want %v", got, want)
			}
			if len(want) < 3 {
				time.Sleep(time.Second)
			}
		}
	})
}

// TestCountdownTimerParity_OnExpireFiresAtZeroOrBelow locks contract (3):
// onExpire fires when remainingSeconds <= 0. Upstream countdown-timer.ts:29-32:
//
//	if (this.remainingSeconds <= 0) {
//	    this.dispose();
//	    this.onExpire();
//	}
func TestCountdownTimerParity_OnExpireFiresAtZeroOrBelow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		expired := make(chan struct{}, 1)
		ct := NewCountdownTimer(1*time.Second, nil, func(int) {}, func() {
			expired <- struct{}{}
		})
		defer ct.Dispose()

		synctest.Wait()
		time.Sleep(999 * time.Millisecond)
		synctest.Wait()
		select {
		case <-expired:
			t.Fatal("onExpire fired before the first one-second interval")
		default:
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		select {
		case <-expired:
		default:
			t.Fatal("onExpire did not fire at the first one-second interval")
		}
	})
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
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var ticks atomic.Int32
		var expires atomic.Int32
		ct := NewCountdownTimer(5*time.Second, nil,
			func(int) { ticks.Add(1) },
			func() { expires.Add(1) },
		)
		// Initial tick fires synchronously inside the constructor.
		if got := ticks.Load(); got != 1 {
			t.Fatalf("pre-dispose ticks = %d, want 1 (initial only)", got)
		}
		synctest.Wait()
		ct.Dispose()
		time.Sleep(5 * time.Second) // Advance through the original expiration.
		synctest.Wait()
		if got := ticks.Load(); got != 1 {
			t.Fatalf("post-dispose ticks = %d, want 1 (dispose must halt further ticks)", got)
		}
		if got := expires.Load(); got != 0 {
			t.Fatalf("post-dispose expires = %d, want 0 (dispose must halt expiration)", got)
		}
		// Double-dispose must be a no-op (matches upstream's `if (intervalId)` guard).
		ct.Dispose()
	})
}
