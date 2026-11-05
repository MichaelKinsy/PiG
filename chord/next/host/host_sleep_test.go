package host

import (
	"context"
	"errors"
	"testing"
	"time"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
)

// Ports packages/chord/test/next/host/sleep.test.ts: `sleep` resolving, rejecting with the abort reason, chaining delays
// above the timer limit and leaving no timer behind; `untilAborted`. The fake timers are fakeLoop; a Promise is a goroutine
// the test starts and a channel it reads, since `sleep` blocks.

// sleeping runs sleepOn(loop, delay, ctx) on a goroutine and returns the channel its result arrives on.
func sleeping(loop *fakeLoop, delay time.Duration, ctx context.Context) <-chan error {
	result := make(chan error, 1)
	go func() { result <- sleepOn(loop, delay, ctx) }()
	// The sleeper has taken its timer when the clock can see it (the timer is made before the goroutine blocks).
	for loop.TimerCount() == 0 && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	return result
}

func settled(t *testing.T, result <-chan error) (error, bool) {
	t.Helper()
	select {
	case err := <-result:
		return err, true
	case <-time.After(20 * time.Millisecond):
		return nil, false
	}
}

func TestSleepResolvesAfterItsDelay(t *testing.T) {
	loop := newFakeLoop()
	result := sleeping(loop, time.Second, chordctx.Background())
	loop.Advance(999)
	if _, done := settled(t, result); done {
		t.Fatal("slept less than its delay")
	}
	loop.Advance(1)
	if err, done := settled(t, result); !done || err != nil {
		t.Fatalf("sleep = %v done %v, want nil true", err, done)
	}
	if loop.TimerCount() != 0 {
		t.Fatalf("timers %d, want 0", loop.TimerCount())
	}
}

func TestSleepRejectsWithTheAbortReasonAndClearsItsTimer(t *testing.T) {
	loop := newFakeLoop()
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	result := sleeping(loop, time.Minute, ctx)
	reason := errors.New("stop")
	cancel(reason)
	err, done := settled(t, result)
	if !done || !errors.Is(err, reason) {
		t.Fatalf("sleep = %v done %v, want the reason", err, done)
	}
	if loop.TimerCount() != 0 {
		t.Fatalf("timers %d, want 0", loop.TimerCount())
	}
}

func TestSleepRejectsAtOnceWhenItsContextIsAbortedAlready(t *testing.T) {
	loop := newFakeLoop()
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	cancel(errors.New("before"))
	err := sleepOn(loop, 10*time.Millisecond, ctx)
	if err == nil || err.Error() != "before" {
		t.Fatalf("sleep = %v, want before", err)
	}
	if loop.TimerCount() != 0 {
		t.Fatalf("timers %d, want 0", loop.TimerCount())
	}
}

func TestSleepChainsTimersForDelaysAbove2Pow31Minus1MsAnAbortClearsTheChainedTimer(t *testing.T) {
	loop := newFakeLoop()
	limit := int64(1<<31 - 1)
	result := sleeping(loop, time.Duration(limit+5_000)*time.Millisecond, chordctx.Background())
	loop.Advance(limit)
	if _, done := settled(t, result); done {
		t.Fatal("slept less than its delay")
	}
	if loop.TimerCount() != 1 {
		t.Fatalf("timers %d, want 1", loop.TimerCount())
	}
	loop.Advance(5_000)
	if err, done := settled(t, result); !done || err != nil {
		t.Fatalf("sleep = %v done %v, want nil true", err, done)
	}
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	long := sleeping(loop, time.Duration(3*limit)*time.Millisecond, ctx)
	loop.Advance(limit + 1)
	cancel(errors.New("late"))
	err, done := settled(t, long)
	if !done || err == nil || err.Error() != "late" {
		t.Fatalf("sleep = %v done %v, want late", err, done)
	}
	if loop.TimerCount() != 0 {
		t.Fatalf("timers %d, want 0", loop.TimerCount())
	}
}

func TestUntilAbortedResolvesOnceItsContextIsAbortedAtOnceWhenItAlreadyIs(t *testing.T) {
	ctx, cancel := chordctx.WithCancel(chordctx.Background())
	done := make(chan struct{})
	go func() {
		UntilAborted(ctx)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("UntilAborted returned before its context was aborted")
	case <-time.After(20 * time.Millisecond):
	}
	cancel(nil)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("UntilAborted did not return after the abort")
	}
	UntilAborted(ctx)
}
