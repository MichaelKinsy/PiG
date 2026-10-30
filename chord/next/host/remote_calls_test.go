package host

import (
	"runtime"
	"slices"
	"testing"
	"weak"

	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
)

// Ports packages/chord/test/next/remote/calls.test.ts.

// testCall is a call of hold that records its name in sent when it goes out.
type testCall struct {
	name string
	hold int
	sent *[]string
}

func callOf(name string, sent *[]string, hold ...int) *testCall {
	call := &testCall{name: name, sent: sent}
	if len(hold) > 0 {
		call.hold = hold[0]
	}
	return call
}

func (c *testCall) holdID() int { return c.hold }
func (c *testCall) send()       { *c.sent = append(*c.sent, c.name) }

func expectSent(t *testing.T, sent []string, want ...string) {
	t.Helper()
	if !slices.Equal(sent, want) {
		t.Fatalf("sent = %v, want %v", sent, want)
	}
}

// calls.test.ts: the call window › sends nothing before the peer's hello, then up to its N; the rest waits
func TestCallWindowSendsNothingBeforeHelloThenUpToN(t *testing.T) {
	var sent []string
	window := &callWindow[*testCall]{}
	for _, name := range []string{"a", "b", "c"} {
		window.submit(callOf(name, &sent))
	}
	expectSent(t, sent)
	window.opened(2)
	expectSent(t, sent, "a", "b")
	window.freed()
	expectSent(t, sent, "a", "b", "c")
}

// calls.test.ts: the call window › sends waiting calls in order as slots free
func TestCallWindowSendsWaitingCallsInOrder(t *testing.T) {
	var sent []string
	window := &callWindow[*testCall]{}
	window.opened(1)
	for _, name := range []string{"a", "b", "c", "d"} {
		window.submit(callOf(name, &sent))
	}
	for range 3 {
		window.freed()
	}
	expectSent(t, sent, "a", "b", "c", "d")
}

// calls.test.ts: the call window › never preempts: a call that waits does not overtake while every slot is taken
func TestCallWindowNeverPreempts(t *testing.T) {
	var sent []string
	window := &callWindow[*testCall]{}
	window.opened(1)
	window.submit(callOf("long", &sent))
	window.submit(callOf("short", &sent))
	expectSent(t, sent, "long")
}

// calls.test.ts: the call window › takes out a cancelled waiting call, and every waiting call of a hold that rebinds
func TestCallWindowTakesOutCancelledAndRebindingCalls(t *testing.T) {
	var sent []string
	window := &callWindow[*testCall]{}
	window.opened(1)
	window.submit(callOf("first", &sent))
	cancelled := callOf("cancelled", &sent)
	window.submit(cancelled)
	rebinding := []*testCall{callOf("hold 1 a", &sent, 1), callOf("hold 1 b", &sent, 1)}
	for _, call := range rebinding {
		window.submit(call)
	}
	window.submit(callOf("last", &sent))
	window.remove(cancelled)
	// A second remove of the same call changes nothing.
	window.remove(cancelled)
	if removed := window.removeHold(1); !slices.Equal(removed, rebinding) {
		t.Fatalf("removeHold(1) = %v, want %v", removed, rebinding)
	}
	window.freed()
	expectSent(t, sent, "first", "last")
	if cleared := window.clear(); len(cleared) != 0 {
		t.Fatalf("clear() = %v, want none", cleared)
	}
}

// calls.test.ts: the call window › keeps no call that went out or was taken
func TestCallWindowKeepsNoCallThatWentOutOrWasTaken(t *testing.T) {
	window := &callWindow[*testCall]{}
	window.opened(1)
	refs := func() []weak.Pointer[testCall] {
		var none []string
		sent, next, waiting := callOf("a", &none), callOf("b", &none), callOf("c", &none)
		for _, call := range []*testCall{sent, next, waiting} {
			window.submit(call)
		}
		window.freed()
		window.remove(waiting)
		return []weak.Pointer[testCall]{weak.Make(sent), weak.Make(waiting)}
	}()
	nexttest.ExpectCollected(t, refs...)
	runtime.KeepAlive(window)
}
