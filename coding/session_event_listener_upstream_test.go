package coding

import (
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
)

// packages/coding-agent/src/core/agent-session.ts:240,1369-1378: subscribe(listener: AgentSessionEventListener) returns an unsubscribe that removes only that listener, so a later turn reaches the remaining listener and not the removed one.
func TestSessionSubscribeAgentSessionEventListenerUnsubscribesOnlyItself(t *testing.T) {
	h := newBoundaryHarness(t, harnessOptions{}, boundaryReply("one", ai.StopReasonStop, 0), boundaryReply("two", ai.StopReasonStop, 0))
	var removed, kept int
	var removedListener AgentSessionEventListener = func(agent.AgentEvent) { removed++ }
	var keptListener AgentSessionEventListener = func(agent.AgentEvent) { kept++ }
	unsubscribe := h.session.Subscribe(removedListener)
	h.session.Subscribe(keptListener)
	boundaryPrompt(t, h, "first")
	if removed == 0 || kept != removed {
		t.Fatalf("both listeners must see the first turn: removed=%d kept=%d", removed, kept)
	}
	unsubscribe()
	before := removed
	boundaryPrompt(t, h, "second")
	if removed != before {
		t.Fatalf("an unsubscribed listener saw %d more events", removed-before)
	}
	if kept <= before {
		t.Fatalf("the remaining listener must still see the second turn: kept=%d before=%d", kept, before)
	}
}

// agent-session.ts:1369-1378 subscribe pushes the listener on `_eventListeners` and the unsubscribe splices out only that listener; the emit loop calls the
// listeners in subscription order, and a listener subscribed later does not displace the earlier ones.
func TestSessionSubscribeNotifiesEveryListenerInOrderAndUnsubscribeLeavesTheOthers(t *testing.T) {
	h := newBoundaryHarness(t, harnessOptions{})
	var calls []string
	listener := func(name string) AgentSessionEventListener {
		return func(agent.AgentEvent) { calls = append(calls, name) }
	}
	h.session.Subscribe(listener("a"))
	unsubscribeB := h.session.Subscribe(listener("b"))
	h.session.Subscribe(listener("c"))
	noop := h.session.Subscribe(nil)

	event := agent.AgentEvent(agent.AgentStartEvent{})
	h.session.notifyListeners(event)
	if want := []string{"a", "b", "c"}; !slices.Equal(calls, want) {
		t.Fatalf("calls = %v, want every listener in subscription order %v", calls, want)
	}

	noop()
	calls = nil
	unsubscribeB()
	unsubscribeB()
	h.session.notifyListeners(event)
	if want := []string{"a", "c"}; !slices.Equal(calls, want) {
		t.Fatalf("after unsubscribing b, calls = %v, want %v", calls, want)
	}
}

// agent-session.ts:1039-1043 _emit iterates the live _eventListeners array (for...of), and unsubscribe splices it (:1370-1378). Probed in node with
// that code: a listener that unsubscribes the next one gives a,c; one that unsubscribes itself shifts the next into its slot, which is skipped (a,c);
// and a listener subscribed during delivery is called in the same delivery (a,b,d).
func TestSessionNotifyListenersWalksTheLiveListenerListAsPi(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(h *recoveryHarness, calls *[]string)
		want  []string
	}{
		{"a unsubscribes b", func(h *recoveryHarness, calls *[]string) {
			var unsubscribeB func()
			h.session.Subscribe(func(agent.AgentEvent) { *calls = append(*calls, "a"); unsubscribeB() })
			unsubscribeB = h.session.Subscribe(func(agent.AgentEvent) { *calls = append(*calls, "b") })
			h.session.Subscribe(func(agent.AgentEvent) { *calls = append(*calls, "c") })
		}, []string{"a", "c"}},
		{"a unsubscribes itself", func(h *recoveryHarness, calls *[]string) {
			var unsubscribeA func()
			unsubscribeA = h.session.Subscribe(func(agent.AgentEvent) { *calls = append(*calls, "a"); unsubscribeA() })
			h.session.Subscribe(func(agent.AgentEvent) { *calls = append(*calls, "b") })
			h.session.Subscribe(func(agent.AgentEvent) { *calls = append(*calls, "c") })
		}, []string{"a", "c"}},
		{"a subscribes d", func(h *recoveryHarness, calls *[]string) {
			subscribed := false
			h.session.Subscribe(func(agent.AgentEvent) {
				*calls = append(*calls, "a")
				if !subscribed {
					subscribed = true
					h.session.Subscribe(func(agent.AgentEvent) { *calls = append(*calls, "d") })
				}
			})
			h.session.Subscribe(func(agent.AgentEvent) { *calls = append(*calls, "b") })
		}, []string{"a", "b", "d"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newBoundaryHarness(t, harnessOptions{})
			var calls []string
			tc.setup(h, &calls)
			h.session.notifyListeners(agent.AgentStartEvent{})
			if !slices.Equal(calls, tc.want) {
				t.Fatalf("calls = %v, want %v", calls, tc.want)
			}
		})
	}
}
