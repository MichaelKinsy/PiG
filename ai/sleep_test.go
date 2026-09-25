package ai

// Covers .upstream/current/packages/ai/src/utils/sleep.ts, which PiG
// implements as abortableSleep (ai/provider_retry.go): reject at once when
// already aborted, resolve after the delay, reject when aborted mid-wait.

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"
)

func TestAbortableSleepResolvesAfterDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		if err := abortableSleep(context.Background(), 1500*time.Millisecond); err != nil {
			t.Fatalf("err = %v", err)
		}
		if elapsed := time.Since(start); elapsed != 1500*time.Millisecond {
			t.Fatalf("slept %v, want 1.5s", elapsed)
		}
	})
}

func TestAbortableSleepRejectsWhenAlreadyAborted(t *testing.T) {
	for _, delay := range []time.Duration{0, time.Second} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := abortableSleep(ctx, delay); !errors.Is(err, context.Canceled) {
			t.Fatalf("delay %v: err = %v, want context.Canceled", delay, err)
		}
	}
}

func TestAbortableSleepRejectsWhenAbortedMidWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		start := time.Now()
		time.AfterFunc(200*time.Millisecond, cancel)
		if err := abortableSleep(ctx, time.Minute); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if elapsed := time.Since(start); elapsed != 200*time.Millisecond {
			t.Fatalf("returned after %v, want at the 200ms abort", elapsed)
		}
	})
}
