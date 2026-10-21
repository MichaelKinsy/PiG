package extensionconformance

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// deferredNotices is how many notifications of deferred work (a timer, an immediate, a thread or a goroutine the tool left running) the UI recorded.
func deferredNotices(h *harness) int {
	n := 0
	for _, notice := range h.ui.Recorded() {
		if strings.HasPrefix(notice, "deferred:") {
			n++
		}
	}
	return n
}

// timerDeferWant is how many deferred and inline notifications timer_defer makes in an SDK. Node's tool queues a setImmediate and a setTimeout(0) callback and
// notifies from the handler, a microtask and a promise continuation; the others notify from the handler and, once timer_release lets it, from deferred work.
func timerDeferWant(name string) (deferred, inline int) {
	if strings.HasPrefix(name, "subprocess-node") {
		return 2, 3
	}
	return 1, 1
}

// D5: Pi runs the callback of a timer or an immediate once its event loop turns, which a chain of microtasks does not give it. An extension runtime that is another process runs
// the callback at once, so each runtime marks the host calls of deferred work (CallPayload.Timer) and the host holds them. Node marks the calls of a setImmediate or setTimeout
// callback that fires after the handler returned; the Go, Rust and Python SDKs mark a call made through a request's context after the handler returned. Calls the handler
// and its microtask and promise continuations make carry no mark. Probed against Pi: those continuations notify before tool_execution_end, the callbacks after agent_settled.
func TestDeferredHostCallsCarryTheTimerMarkAcrossSDKs(t *testing.T) {
	for _, tc := range sdkHarnessCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			h.wire.on.Store(true)
			wantDeferred, wantInline := timerDeferWant(tc.name)
			results := dispatchToolCalls(t, h, []ai.FauxContentBlock{ai.FauxToolCall("timer_defer", map[string]any{}, &ai.FauxToolCallOptions{ID: "timer-defer-1"})})
			if len(results) != 1 || results[0].IsError {
				t.Fatalf("timer_defer: %s", describeToolResults(results))
			}
			// The handler returned. The tools that wait for a release have their deferred work run now.
			results = dispatchToolCalls(t, h, []ai.FauxContentBlock{ai.FauxToolCall("timer_release", map[string]any{}, &ai.FauxToolCallOptions{ID: "timer-release-1"})})
			if len(results) != 1 || results[0].IsError {
				t.Fatalf("timer_release: %s", describeToolResults(results))
			}
			deadline := time.Now().Add(60 * time.Second)
			var notices []wireCall
			for {
				notices = append(notices, h.wire.take("ui.notify")...)
				deferred := 0
				for _, call := range notices {
					if strings.Contains(string(call.args), `"deferred:`) {
						deferred++
					}
				}
				if deferred >= wantDeferred {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("saw %d deferred notifications, want %d", deferred, wantDeferred)
				}
				time.Sleep(10 * time.Millisecond)
			}
			var deferred, inline int
			for _, call := range notices {
				var args struct {
					Message string `json:"message"`
				}
				if err := json.Unmarshal(call.args, &args); err != nil {
					t.Fatalf("decode ui.notify %s: %v", call.args, err)
				}
				switch {
				case strings.HasPrefix(args.Message, "deferred:"):
					deferred++
					if !call.timer {
						t.Errorf("%s was made by deferred work after the handler returned and carries no timer mark", args.Message)
					}
				case strings.HasPrefix(args.Message, "inline:"):
					inline++
					if call.timer {
						t.Errorf("%s was made by the handler or its continuation and carries a timer mark", args.Message)
					}
				}
			}
			if deferred != wantDeferred || inline != wantInline {
				t.Fatalf("saw %d deferred and %d inline notifications, want %d and %d", deferred, inline, wantDeferred, wantInline)
			}
		})
	}
}

// D5: the host holds the calls of deferred work while the agent's run chains microtasks, so they reach the UI after the run's own output, as Pi's loop runs the callbacks once the run
// is over. A Node tool that queued a setImmediate and a setTimeout(0) callback returns; its callbacks fire while the run still awaits the tool's result hooks and the next model request,
// and their notifications must not reach the UI before tool_execution_end or before the chain ends.
func TestNodeTimerCallbackNotificationsWaitForTheEndOfTheRun(t *testing.T) {
	for _, tc := range sdkHarnessCases() {
		if !strings.HasPrefix(tc.name, "subprocess-node") {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			var turns jsloop.Turns
			h.host.SetTimerTurns(&turns)
			tools, errs := coding.BridgeNewRunnerTools(h.runner.Tools())
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			provider.SetResponses([]ai.FauxResponseStep{
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxToolCall("timer_defer", map[string]any{}, &ai.FauxToolCallOptions{ID: "timer-defer-1"})}, StopReason: "toolUse"}),
				ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
			})
			a := mustNewAgent(agent.AgentOptions{Model: &ai.Model{ID: "faux-1", Provider: provider}, Tools: tools})
			a.SetLoopTurns(&turns)
			deferredAtToolEnd := -1
			a.Subscribe(func(_ context.Context, event agent.AgentEvent) error {
				if _, ok := event.(agent.ToolExecutionEndEvent); ok {
					deferredAtToolEnd = deferredNotices(h)
				}
				return nil
			})
			end := turns.Chain()
			if _, err := a.Send(t.Context(), "go"); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(60 * time.Second)
			for turns.Held() < 2 {
				if time.Now().After(deadline) {
					t.Fatalf("held %d calls, want the 2 calls of the callbacks (the host ran them as they arrived: %d reached the UI)", turns.Held(), deferredNotices(h))
				}
				time.Sleep(10 * time.Millisecond)
			}
			if deferredAtToolEnd != 0 || deferredNotices(h) != 0 {
				t.Fatalf("deferred notifications reached the UI at tool_execution_end (%d) or before the chain ended (%d)", deferredAtToolEnd, deferredNotices(h))
			}
			end()
			for deferredNotices(h) < 2 {
				if time.Now().After(deadline) {
					t.Fatalf("the held notifications never reached the UI after the chain ended: %v", h.ui.Recorded())
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
