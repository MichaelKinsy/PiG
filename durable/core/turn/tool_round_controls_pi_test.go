package turn_test

// pi: packages/durable/src/harness/generation.ts (finishToolRound :594-650)

import (
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/session"
	"github.com/MichaelKinsy/PiG/durable/core/turn"
)

func controlResult(control string) string {
	if control == "" {
		return `{"content":[]}`
	}
	return `{"content":[],"control":` + control + `}`
}

// toolRoundWith runs one generation that calls the echo tool once (calls = 1) or twice (calls = 2) and settles each call with the given tool result.
func toolRoundWith(t *testing.T, r *rig, results ...string) {
	t.Helper()
	r.agent.Tools = []turn.Tool{echoTool()}
	r.start("hi")
	r.resume()
	if len(results) == 2 {
		r.finish(twoCalls)
	} else {
		r.finish(toolUseWith("echo", `{}`))
	}
	if len(r.toolRuns) != len(results) {
		t.Fatalf("tool runs = %d, want %d", len(r.toolRuns), len(results))
	}
	for i, result := range results {
		r.toolRuns[i].then(mustParse(t, result), nil)
	}
	r.run()
}

// generation.ts:613-616 and :625: a tool round ends the run at once (no further model request) only when every call of the round returned `control.terminate: true`; one call without it hands the run to the next generation.
// mutation-checked: ending on any single terminate, never ending, or treating a missing task as terminating fails the cases.
func TestToolRoundEndsTheRunOnlyWhenEveryCallAsksToTerminate(t *testing.T) {
	terminate := controlResult(`{"terminate":true}`)
	for _, tc := range []struct {
		name         string
		results      []string
		wantRequests int
	}{
		{"one call terminates", []string{terminate}, 0},
		{"one call without control", []string{controlResult("")}, 1},
		{"terminate false", []string{controlResult(`{"terminate":false}`)}, 1},
		{"both calls terminate", []string{terminate, terminate}, 0},
		{"only the first call terminates", []string{terminate, controlResult("")}, 1},
		{"only the second call terminates", []string{controlResult(""), terminate}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			toolRoundWith(t, r, tc.results...)
			if len(r.requests) != tc.wantRequests {
				t.Errorf("model requests after the round = %d, want %d", len(r.requests), tc.wantRequests)
			}
		})
	}
}

// generation.ts:617 and :622: the `addTools` of every call's control, in call order, are added to the agent's loadout in the round's closing commit; a round without any adds nothing.
// mutation-checked: skipping the add, or passing only the last call's tools, fails it.
func TestToolRoundAddsTheToolsItsControlsAsk(t *testing.T) {
	for _, tc := range []struct {
		name    string
		results []string
		want    string
	}{
		{"two calls add", []string{controlResult(`{"addTools":["a"]}`), controlResult(`{"addTools":["b","c"]}`)}, `["a","b","c"]`},
		{"none add", []string{controlResult(""), controlResult(`{"terminate":false}`)}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			var added []any
			calls := 0
			r.host.AddTools = func(_ *session.Tx, _ session.ID, tools []any) error {
				calls++
				added = append(added, tools...)
				return nil
			}
			toolRoundWith(t, r, tc.results...)
			if tc.want == "" {
				if calls != 0 {
					t.Errorf("AddTools was called %d time(s) for a round that adds nothing", calls)
				}
				return
			}
			if got := jv.Stringify(added); got != tc.want {
				t.Errorf("added tools = %s, want %s", got, tc.want)
			}
		})
	}
}

// generation.ts:618-631: the last `handoff` in call order wins; the round appends a `pi.reset` entry holding it as a user message, which cuts the context, and ends the run without another model request.
// mutation-checked: the first handoff winning, not appending the reset, or continuing the run fails it.
func TestToolRoundHandoffAppendsAResetAndEndsTheRun(t *testing.T) {
	r := newRig(t)
	toolRoundWith(t, r, controlResult(`{"handoff":"first"}`), controlResult(`{"handoff":"second"}`))
	if len(r.requests) != 0 {
		t.Errorf("model requests after a handoff = %d, want the run ended", len(r.requests))
	}
	last := lastEntry(r)
	if !strings.Contains(last, `"kind":"pi.reset"`) || !strings.Contains(last, `"content":"second"`) || strings.Contains(last, "first") {
		t.Errorf("last entry = %s, want a reset carrying the last handoff", last)
	}
}

// generation.ts:613-616 with the sequential round (:560-590): each call started after the first links its task to its live slot, so a round whose calls all terminate still ends the run; a slot without a task never terminates.
// mutation-checked: not linking the later call's task to its slot, or linking the wrong slot, keeps the run going and fails it.
func TestSequentialToolRoundLinksEachLaterCallToItsSlotBeforeDecidingToTerminate(t *testing.T) {
	r := newRig(t)
	r.settings.ToolExecution = "sequential"
	r.agent.Tools = []turn.Tool{echoTool()}
	r.start("hi")
	r.resume()
	r.finish(twoCalls)
	if len(r.toolRuns) != 1 {
		t.Fatalf("tool runs before the first call settles = %d, want 1", len(r.toolRuns))
	}
	r.toolRuns[0].then(mustParse(t, controlResult(`{"terminate":true}`)), nil)
	r.run()
	if len(r.toolRuns) != 2 {
		t.Fatalf("tool runs after the first call settled = %d, want the second started", len(r.toolRuns))
	}
	r.toolRuns[1].then(mustParse(t, controlResult(`{"terminate":true}`)), nil)
	r.run()
	if len(r.requests) != 0 {
		t.Errorf("model requests after every call terminated = %d, want the run ended", len(r.requests))
	}
}
