package app

// pi: packages/chord/src/node/app/ephemeral-sign-ins.ts
// pi: packages/chord/src/node/app/ephemeral-sessions.ts

import (
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/test/node/app/ephemeral-sign-ins.test.ts (Pi main c23b9ec1; the names of Pi ff7414d51): at most max, the oldest ending past it; each
// ends once its last link closed and none opened for the grace period, or when none opened within it of the login.
//
// Go forced difference: Pi's tests drive fake timers; Go has none, so the sign-ins' timer is a seam (afterFunc) and a test fires
// the fake timers by hand.

var ephemeralPrincipal = delta.JsonObjectOf("kind", "user", "id", "u", "name", "u")

// fakeTimers is a clock the test advances by hand.
type fakeTimers struct {
	mu      sync.Mutex
	now     time.Duration
	pending []*fakeTimer
}

type fakeTimer struct {
	at      time.Duration
	fn      func()
	stopped bool
}

func (t *fakeTimer) Stop() bool {
	was := !t.stopped
	t.stopped = true
	return was
}

func (f *fakeTimers) after(d time.Duration, fn func()) timerHandle {
	f.mu.Lock()
	defer f.mu.Unlock()
	timer := &fakeTimer{at: f.now + d, fn: fn}
	f.pending = append(f.pending, timer)
	return timer
}

// advance moves the clock by d and runs the timers that came due, in order.
func (f *fakeTimers) advance(d time.Duration) {
	f.mu.Lock()
	f.now += d
	var due []*fakeTimer
	var rest []*fakeTimer
	for _, timer := range f.pending {
		switch {
		case timer.stopped:
		case timer.at <= f.now:
			due = append(due, timer)
		default:
			rest = append(rest, timer)
		}
	}
	f.pending = rest
	f.mu.Unlock()
	for _, timer := range due {
		if !timer.stopped {
			timer.fn()
		}
	}
}

func ephemeralWith(max int, grace time.Duration) (*EphemeralSignIns, *fakeTimers) {
	timers := &fakeTimers{}
	signIns := NewEphemeralSignIns(max, grace)
	signIns.after = timers.after
	return signIns, timers
}

func alive(signIns *EphemeralSignIns, id string) bool {
	_, _, ok := signIns.Get(id)
	return ok
}

func TestEphemeralSignInsEndWithTheirLastLinkAfterTheGracePeriodALinkOpeningMeanwhileKeepsThem(t *testing.T) {
	signIns, timers := ephemeralWith(10, time.Second)
	signIns.Issue("a", "hash", ephemeralPrincipal)
	first, second := signIns.LinkOpened("a"), signIns.LinkOpened("a")
	first()
	timers.advance(5 * time.Second)
	if !alive(signIns, "a") {
		t.Fatal("a sign-in with an open link ended")
	}
	second()
	second()
	timers.advance(999 * time.Millisecond)
	again := signIns.LinkOpened("a")
	timers.advance(5 * time.Second)
	if !alive(signIns, "a") {
		t.Fatal("a sign-in with a reopened link ended")
	}
	again()
	timers.advance(time.Second)
	if alive(signIns, "a") {
		t.Fatal("the sign-in outlived its grace period")
	}
	if signIns.LinkOpened("a") != nil {
		t.Fatal("a link opened on an ended sign-in")
	}
}

func TestEphemeralSignInsEndWhenNoLinkOpensWithinTheGracePeriodOfTheLogin(t *testing.T) {
	signIns, timers := ephemeralWith(10, time.Second)
	signIns.Issue("a", "hash", ephemeralPrincipal)
	timers.advance(time.Second)
	if alive(signIns, "a") {
		t.Fatal("a sign-in nobody connected to lived on")
	}
}

func TestEphemeralSignInsKeepAtMostMaxPastItTheOldestEnds(t *testing.T) {
	signIns, _ := ephemeralWith(2, time.Minute)
	for _, id := range []string{"a", "b", "c"} {
		signIns.Issue(id, "hash", ephemeralPrincipal)
	}
	if alive(signIns, "a") || !alive(signIns, "b") || !alive(signIns, "c") {
		t.Fatalf("alive: a %v b %v c %v", alive(signIns, "a"), alive(signIns, "b"), alive(signIns, "c"))
	}
	if !signIns.Revoke("b") || signIns.Revoke("b") {
		t.Fatal("Revoke b: want true then false")
	}
	signIns.Revoke("c")
}
