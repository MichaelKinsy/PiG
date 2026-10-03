package coding

import (
	"context"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
)

// Pi binds an extension's ctx.signal to the session's `this.agent.signal` (agent-session.ts:3368): the signal of the run in progress, undefined when none is active (agent.ts:336-338).
// Every handler of one run sees the same signal, a run's abort cancels it, and it is cleared before agent_settled.
func TestSessionBindsExtensionSignalToTheActiveRun(t *testing.T) {
	type observation struct {
		event  string
		signal context.Context
	}
	var observed []observation
	var h *recoveryHarness
	record := func(event string, abort bool) extension.HandlerFn {
		return func(args ...any) (any, error) {
			signal, err := extension.FromContext(args[1].(context.Context)).Signal()
			if err != nil {
				t.Error(err)
			}
			observed = append(observed, observation{event, signal})
			if abort {
				h.session.RequestAbort()
			}
			return nil, nil
		}
	}
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"agent_start":   {record("agent_start", false)},
		"turn_start":    {record("turn_start", true)},
		"agent_end":     {record("agent_end", false)},
		"agent_settled": {record("agent_settled", false)},
	}}
	h = newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("done", ai.StopReasonStop, 0))
	h.session.bindExtensionCore(h.session.currentRunner())
	boundaryPrompt(t, h, "start")

	var events []string
	for _, o := range observed {
		events = append(events, o.event)
	}
	if len(observed) < 4 || events[0] != "agent_start" || events[1] != "turn_start" || events[len(events)-2] != "agent_end" || events[len(events)-1] != "agent_settled" {
		t.Fatalf("events = %v, want agent_start, turn_start ... agent_end, agent_settled", events)
	}
	run := observed[0].signal
	if run == nil {
		t.Fatal("ctx.signal is nil during agent_start")
	}
	for _, o := range observed[:len(observed)-1] {
		if o.signal != run {
			t.Errorf("%s saw a different signal than agent_start", o.event)
		}
	}
	if run.Err() == nil {
		t.Error("the run's signal was not aborted by the abort the turn_start handler requested")
	}
	if last := observed[len(observed)-1]; last.signal != nil {
		t.Errorf("agent_settled saw signal %v, want nil: Pi clears the run before it", last.signal)
	}
}
