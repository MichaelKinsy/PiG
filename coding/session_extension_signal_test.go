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

// upstream: packages/coding-agent/src/core/agent-session.ts:4373 (hasExtensionHandlers returns this._extensionRunner.hasHandlers(eventType)) over runner.ts hasHandlers: true exactly for the event types a loaded extension registered a handler for.
func TestSessionHasExtensionHandlersReportsRegisteredEvents(t *testing.T) {
	ext := extension.Extension{Handlers: map[string][]extension.HandlerFn{
		"agent_start": {func(...any) (any, error) { return nil, nil }},
		"turn_end":    {},
	}}
	h := newBoundaryHarness(t, harnessOptions{extension: ext}, boundaryReply("done", ai.StopReasonStop, 0))
	for event, want := range map[string]bool{"agent_start": true, "turn_end": false, "agent_end": false, "": false} {
		if got := h.session.HasExtensionHandlers(event); got != want {
			t.Errorf("HasExtensionHandlers(%q) = %t, want %t", event, got, want)
		}
	}
	none := newBoundaryHarness(t, harnessOptions{}, boundaryReply("done", ai.StopReasonStop, 0))
	if none.session.HasExtensionHandlers("agent_start") {
		t.Error("a Session with no extension reports a handler")
	}
}

// upstream: agent-session.ts:1658 (promptTemplates returns this._resourceLoader.getPrompts().prompts): the Session reads the templates of its current resource loader, so a loader replaced through SetPromptResources is what the getter shows.
func TestSessionPromptTemplatesReadTheResourceLoader(t *testing.T) {
	h := newBoundaryHarness(t, harnessOptions{}, boundaryReply("done", ai.StopReasonStop, 0))
	if got := h.session.PromptTemplates(); len(got) != 0 {
		t.Fatalf("PromptTemplates = %+v, want none", got)
	}
	want := []PromptTemplate{{Name: "review", Description: "Review code", Content: "Review $1"}, {Name: "fix", Content: "Fix it"}}
	h.session.SetPromptResources(want, nil)
	got := h.session.PromptTemplates()
	if len(got) != 2 || got[0].Name != "review" || got[0].Content != "Review $1" || got[1].Name != "fix" {
		t.Fatalf("PromptTemplates = %+v, want %+v", got, want)
	}
	if got[0].Name != h.session.ResourceLoader().GetPrompts().Prompts[0].Name {
		t.Fatal("PromptTemplates disagrees with the loader")
	}
}
