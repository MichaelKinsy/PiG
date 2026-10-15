package turn

// pi: packages/durable/src/harness/output.ts (Progress :268-341)

import (
	"errors"
	"testing"
)

type progressClock struct {
	now    float64
	timers []progressTimer
}

type progressTimer struct {
	wait float64
	fn   func()
}

func (c *progressClock) host() *Host {
	return &Host{
		Now: func() float64 { return c.now },
		After: func(wait float64, fn func()) func() {
			c.timers = append(c.timers, progressTimer{wait, fn})
			return func() {}
		},
	}
}

// output.ts:308-317 #schedule: the first change commits at once; a change within the pause of the previous commit (min interval, or its size at 100 KiB/s) waits for a timer of exactly the remaining pause; one at the moment the pause ends
// commits at once (`wait <= 0`).
// mutation-checked: `wait < 0` for `wait <= 0` schedules a zero timer at the boundary and fails the last step.
func TestProgressCommitsAtOnceWhenIdleAndAfterThePauseOtherwiseWaitsForItsRemainder(t *testing.T) {
	clock := &progressClock{now: 1000}
	var written []int
	p := newProgress(clock.host(), func(then func(int, error)) {
		written = append(written, len(written)+1)
		then(0, nil)
	}, func(error) {}, 100)
	p.mark()
	if len(written) != 1 || len(clock.timers) != 0 {
		t.Fatalf("first mark: commits = %d, timers = %d, want an immediate commit", len(written), len(clock.timers))
	}
	// The commit started at 1000 with min interval 100: the next may start at 1100.
	clock.now = 1040
	p.mark()
	if len(written) != 1 || len(clock.timers) != 1 || clock.timers[0].wait != 60 {
		t.Fatalf("mark inside the pause: commits = %d, timers = %+v, want one timer of 60 ms", len(written), clock.timers)
	}
	clock.now = 1100
	clock.timers[0].fn()
	if len(written) != 2 {
		t.Fatalf("after the timer: commits = %d, want 2", len(written))
	}
	// Second commit started at 1100: the pause ends at 1200; a mark exactly then commits at once.
	clock.now = 1200
	clock.timers = nil
	p.mark()
	if len(written) != 3 || len(clock.timers) != 0 {
		t.Errorf("mark at the end of the pause: commits = %d, timers = %+v, want an immediate commit", len(written), clock.timers)
	}
}

// output.ts:319-322 #flush and :330-333: a flush with nothing dirty, or after stop, writes nothing; a failed commit reports its error, rejects its waiters and pauses the next commit by the min interval.
// mutation-checked: `&&` for `||` in the stopped/dirty guard writes on a clean progress and fails it.
func TestProgressFlushWritesOnlyWhatIsDirtyAndReportsAFailedCommit(t *testing.T) {
	clock := &progressClock{now: 0}
	writes := 0
	failure := errors.New("commit failed")
	var reported error
	var waited error
	p := newProgress(clock.host(), func(then func(int, error)) {
		writes++
		then(0, failure)
	}, func(err error) { reported = err }, 100)
	p.flush()
	if writes != 0 {
		t.Fatalf("a clean progress wrote %d time(s)", writes)
	}
	p.markAndWait(func(err error) { waited = err })
	if writes != 1 || !errors.Is(reported, failure) || !errors.Is(waited, failure) {
		t.Fatalf("writes = %d, reported = %v, waiter = %v, want the failure reported to both", writes, reported, waited)
	}
	if p.nextAt != 100 {
		t.Errorf("next commit at %v after a failed commit, want the min interval (100)", p.nextAt)
	}
	p.stop(func([]func(error)) {})
	p.dirty = true
	p.flush()
	if writes != 1 {
		t.Errorf("a stopped progress wrote")
	}
}
