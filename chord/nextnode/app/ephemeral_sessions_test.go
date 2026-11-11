package app

// pi: packages/chord/src/node/app/ephemeral-sessions.ts

import (
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/delta"
)

// Ports packages/chord/test/node/app/ephemeral-sessions.test.ts (Pi main c23b9ec1): at most max, the oldest ending past it; each
// ends once its last link closed and none opened for the grace period, or when none opened within it of the login.
//
// Go forced difference: Pi's tests drive fake timers; Go has none, so the sessions' timer is a seam (afterFunc) and a test fires
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

func ephemeralWith(max int, grace time.Duration) (*EphemeralSessions, *fakeTimers) {
	timers := &fakeTimers{}
	sessions := NewEphemeralSessions(max, grace)
	sessions.after = timers.after
	return sessions, timers
}

func alive(sessions *EphemeralSessions, id string) bool {
	_, _, ok := sessions.Get(id)
	return ok
}

func TestEphemeralSessionsEndWithTheirLastLinkAfterTheGracePeriodALinkOpeningMeanwhileKeepsThem(t *testing.T) {
	sessions, timers := ephemeralWith(10, time.Second)
	sessions.Issue("a", "hash", ephemeralPrincipal)
	first, second := sessions.LinkOpened("a"), sessions.LinkOpened("a")
	first()
	timers.advance(5 * time.Second)
	if !alive(sessions, "a") {
		t.Fatal("a session with an open link ended")
	}
	second()
	second()
	timers.advance(999 * time.Millisecond)
	again := sessions.LinkOpened("a")
	timers.advance(5 * time.Second)
	if !alive(sessions, "a") {
		t.Fatal("a session with a reopened link ended")
	}
	again()
	timers.advance(time.Second)
	if alive(sessions, "a") {
		t.Fatal("the session outlived its grace period")
	}
	if sessions.LinkOpened("a") != nil {
		t.Fatal("a link opened on an ended session")
	}
}

func TestEphemeralSessionsEndWhenNoLinkOpensWithinTheGracePeriodOfTheLogin(t *testing.T) {
	sessions, timers := ephemeralWith(10, time.Second)
	sessions.Issue("a", "hash", ephemeralPrincipal)
	timers.advance(time.Second)
	if alive(sessions, "a") {
		t.Fatal("a session nobody connected to lived on")
	}
}

func TestEphemeralSessionsKeepAtMostMaxPastItTheOldestEnds(t *testing.T) {
	sessions, _ := ephemeralWith(2, time.Minute)
	for _, id := range []string{"a", "b", "c"} {
		sessions.Issue(id, "hash", ephemeralPrincipal)
	}
	if alive(sessions, "a") || !alive(sessions, "b") || !alive(sessions, "c") {
		t.Fatalf("alive: a %v b %v c %v", alive(sessions, "a"), alive(sessions, "b"), alive(sessions, "c"))
	}
	if !sessions.Revoke("b") || sessions.Revoke("b") {
		t.Fatal("Revoke b: want true then false")
	}
	sessions.Revoke("c")
}
