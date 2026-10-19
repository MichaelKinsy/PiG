// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package sqlhost

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/abi"
)

// effectSteps answers each event with the next step of a script: one step whose effects carry the given IDs.
type effectSteps struct{ ids [][]uint32 }

func (c *effectSteps) Step(abi.Event) *abi.Step {
	step := &abi.Step{Status: abi.StatusOK}
	if len(c.ids) == 0 {
		return step
	}
	for _, id := range c.ids[0] {
		step.Effects = append(step.Effects, abi.Effect{ID: id, Kind: abi.EffectTimerClear, Payload: abi.U32Payload(1)})
	}
	c.ids = c.ids[1:]
	return step
}

// The host keeps one number, the last effect ID it started, instead of a set of every ID of the handle's life (ABI section 3,
// invariant 6: effect IDs increase). A reissued ID and an ID below the last one, in the same step or a later one, discard
// the handle; increasing IDs across steps do not.
func TestHostDiscardsAHandleWhoseEffectIDsDoNotIncrease(t *testing.T) {
	for _, tc := range []struct {
		name  string
		steps [][]uint32
		fatal bool
	}{
		{"increasing across steps", [][]uint32{{1, 2}, {3}, {7}}, false},
		{"zero first", [][]uint32{{0}, {1}}, false},
		{"reissued in one step", [][]uint32{{1, 1}}, true},
		{"reissued in a later step", [][]uint32{{1, 2}, {2}}, true},
		{"below the last one", [][]uint32{{4}, {3}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := New(Options{Core: &effectSteps{ids: tc.steps}})
			defer func() { _ = h.Close(context.Background()) }()
			var err error
			for range tc.steps {
				if err = h.Send(t.Context(), abi.Event{Kind: abi.EventAPI}); err != nil {
					break
				}
			}
			var fatal *FatalError
			switch {
			case tc.fatal && (!errors.As(err, &fatal) || !strings.Contains(fatal.Message, "issued twice or out of order")):
				t.Fatalf("Send = %v, want the reissued effect ID to discard the handle", err)
			case !tc.fatal && err != nil:
				t.Fatalf("Send = %v, want increasing effect IDs accepted", err)
			}
		})
	}
}

// timerSteps arms a timer that fires at once and one an hour away, then clears the second when asked.
type timerSteps struct {
	fired chan struct{}
	calls int
}

func (c *timerSteps) Step(ev abi.Event) *abi.Step {
	step := &abi.Step{Status: abi.StatusOK}
	if ev.Kind == abi.EventTimer {
		close(c.fired)
		return step
	}
	c.calls++
	switch c.calls {
	case 1:
		step.Effects = []abi.Effect{
			{ID: 1, Kind: abi.EffectTimer, Payload: abi.TimerEffect(1, 1000, abi.TimerVolatile)},
			{ID: 2, Kind: abi.EffectTimer, Payload: abi.TimerEffect(2, 1000+3600e3, abi.TimerVolatile)},
		}
	case 2:
		step.Effects = []abi.Effect{{ID: 3, Kind: abi.EffectTimerClear, Payload: abi.U32Payload(2)}}
	}
	return step
}

// The host's timer map holds the armed timers only: a timer leaves it when it fires and when it is cleared, so the map does
// not grow with every timer of the handle's life (timer IDs are never reused).
func TestHostDropsATimerWhenItFiresAndWhenItIsCleared(t *testing.T) {
	core := &timerSteps{fired: make(chan struct{})}
	h := New(Options{Core: core, Clock: func() float64 { return 1000 }})
	defer func() { _ = h.Close(context.Background()) }()
	armed := func() int {
		n := make(chan int, 1)
		h.enqueue(func(error) { n <- len(h.timers) })
		return <-n
	}
	if err := h.Send(t.Context(), abi.Event{Kind: abi.EventAPI}); err != nil {
		t.Fatal(err)
	}
	<-core.fired
	if got := armed(); got != 1 {
		t.Fatalf("armed timers after the first fired = %d, want 1 (the hour-away one)", got)
	}
	if err := h.Send(t.Context(), abi.Event{Kind: abi.EventAPI}); err != nil {
		t.Fatal(err)
	}
	if got := armed(); got != 0 {
		t.Fatalf("armed timers after the clear = %d, want 0", got)
	}
}

// echoNotices answers each api event with an api_result notice of the event's ID.
type echoNotices struct{}

func (echoNotices) Step(ev abi.Event) *abi.Step {
	return &abi.Step{Status: abi.StatusOK, Notices: []abi.Notice{{Kind: abi.NoticeAPIResult, Payload: append(abi.U32Payload(ev.ID), "body"...)}}}
}

// A notice is tested against the waiters of its own kind and id, then the generic ones, not against every waiter: with 5,000
// keyed waiters, each notice costs one test call of its own waiter, and Cancel, a resolved wait and Close leave nothing behind.
func TestNotifyTestsOnlyTheKeyedWaitersOfTheNotice(t *testing.T) {
	const n = 5000
	h := New(Options{Core: echoNotices{}, Clock: func() float64 { return 1000 }})
	var calls [n + 2]int
	exps := make([]*Expectation, n+2)
	for id := 1; id <= n+1; id++ {
		e, err := h.ExpectNotice(abi.NoticeAPIResult, uint32(id), func(body []byte) bool { calls[id]++; return string(body) == "body" })
		if err != nil {
			t.Fatal(err)
		}
		exps[id] = e
	}
	// A different kind with the same id never reaches an api_result waiter, and a cancelled waiter is gone.
	exps[n+1].Cancel()
	generic := 0
	ge, err := h.Expect(func(abi.Notice) bool { generic++; return false })
	if err != nil {
		t.Fatal(err)
	}
	defer ge.Cancel()
	const sent = 50
	for id := 1; id <= sent; id++ {
		if err := h.Send(t.Context(), abi.Event{Kind: abi.EventAPI, ID: uint32(id)}); err != nil {
			t.Fatal(err)
		}
	}
	for id := 1; id <= sent; id++ {
		if _, err := exps[id].Wait(t.Context()); err != nil {
			t.Fatalf("waiter %d: %v", id, err)
		}
	}
	total := 0
	for id := 1; id <= n+1; id++ {
		total += calls[id]
		want := 0
		if id <= sent {
			want = 1
		}
		if calls[id] != want {
			t.Fatalf("waiter %d tested %d times, want %d", id, calls[id], want)
		}
	}
	if generic != sent {
		t.Fatalf("the generic waiter was tested %d times, want %d", generic, sent)
	}
	// Close fails the keyed waiters still pending.
	if err := h.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := exps[n].Wait(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("pending keyed waiter after Close: %v", err)
	}
	h.mu.Lock()
	left := len(h.byKey) + len(h.waiters)
	h.mu.Unlock()
	if left != 0 {
		t.Fatalf("%d waiters left after Close", left)
	}
}
