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

// tickNotices is how many notifications of process.nextTick callbacks the UI recorded.
func tickNotices(h *harness) int {
	n := 0
	for _, notice := range h.ui.Recorded() {
		if strings.HasPrefix(notice, "tick:") {
			n++
		}
	}
	return n
}

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
func timerDeferWant(name string) (deferred, inline, tick int) {
	if strings.HasPrefix(name, "subprocess-node") {
		return 2, 3, 1
	}
	return 1, 1, 0
}

// D189: Pi runs the callback of a timer or an immediate once its event loop turns, which a chain of microtasks does not give it. An extension runtime that is another process runs
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
			wantDeferred, wantInline, wantTick := timerDeferWant(tc.name)
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
				ticks := 0
				for _, call := range notices {
					if strings.Contains(string(call.args), `"tick:`) {
						ticks++
					}
				}
				if deferred >= wantDeferred && ticks >= wantTick {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("saw %d deferred notifications, want %d", deferred, wantDeferred)
				}
				time.Sleep(10 * time.Millisecond)
			}
			var deferred, inline, tick int
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
					if !call.timer || call.tick {
						t.Errorf("%s was made by deferred work after the handler returned and carries timer=%t tick=%t, want timer only", args.Message, call.timer, call.tick)
					}
				case strings.HasPrefix(args.Message, "tick:"):
					tick++
					if !call.timer || !call.tick {
						t.Errorf("%s was made by a process.nextTick callback after the handler returned and carries timer=%t tick=%t, want both", args.Message, call.timer, call.tick)
					}
				case strings.HasPrefix(args.Message, "inline:"):
					inline++
					if call.timer {
						t.Errorf("%s was made by the handler or its continuation and carries a timer mark", args.Message)
					}
				}
			}
			if deferred != wantDeferred || inline != wantInline || tick != wantTick {
				t.Fatalf("saw %d deferred, %d inline and %d tick notifications, want %d, %d and %d", deferred, inline, tick, wantDeferred, wantInline, wantTick)
			}
		})
	}
}

// D189: the host holds the calls of deferred work while the agent's run chains microtasks, so they reach the UI after the run's own output, as Pi's loop runs the callbacks once the run
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
			deferredAtToolEnd, tickAtToolEnd, tickAfterTicked := -1, -1, -1
			a.Subscribe(func(_ context.Context, event agent.AgentEvent) error {
				if ended, ok := event.(agent.ToolExecutionEndEvent); ok {
					deferredAtToolEnd = deferredNotices(h)
					tickAtToolEnd = tickNotices(h)
					// The RPC mode releases the process.nextTick callbacks of the tool that ended here, as Pi runs them before the tool result message.
					turns.Ticked(ended.ToolCallID)
					tickAfterTicked = tickNotices(h)
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
			if tickAtToolEnd != 0 || tickAfterTicked != 1 {
				t.Fatalf("the process.nextTick notification reached the UI before tool_execution_end (%d) or not once the tool's ticks were released (%d), want 0 then 1", tickAtToolEnd, tickAfterTicked)
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

// D189: a Go, Rust or Python SDK marks every call made through a context whose request returned, and Pi-port extensions keep such a context (an activeCtx) and call through it from a handler that awaits the
// answer. The host holds a marked call until the loop turns, and the turn waits for the handler, so a held call that a running handler awaits hangs every run that reaches it. The host must run such a
// call on arrival: while a handler is pending, Pi's loop is turning. The tool timer_stale awaits a ui.notify made through the context of an earlier timer_defer request while a chain runs and holds calls.
func TestHostCallAwaitedByARunningHandlerIsNotHeldForTheLoopTurn(t *testing.T) {
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
			var turns jsloop.Turns
			h.host.SetTimerTurns(&turns)
			end := turns.Chain()
			defer end()
			if results := dispatchToolCalls(t, h, []ai.FauxContentBlock{ai.FauxToolCall("timer_defer", map[string]any{}, &ai.FauxToolCallOptions{ID: "timer-defer-1"})}); len(results) != 1 || results[0].IsError {
				t.Fatalf("timer_defer: %s", describeToolResults(results))
			}
			type outcome struct {
				results []*agent.ToolResultMessage
			}
			done := make(chan outcome, 1)
			go func() {
				done <- outcome{dispatchToolCalls(t, h, []ai.FauxContentBlock{ai.FauxToolCall("timer_stale", map[string]any{}, &ai.FauxToolCallOptions{ID: "timer-stale-1"})})}
			}()
			select {
			case got := <-done:
				if len(got.results) != 1 || got.results[0].IsError {
					t.Fatalf("timer_stale: %s", describeToolResults(got.results))
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("timer_stale never returned: the host holds the call its handler awaits (%d held) for a loop turn that waits for the handler", turns.Held())
			}
			end()
			deadline := time.Now().Add(60 * time.Second)
			for {
				for _, call := range h.wire.take("ui.notify") {
					if strings.Contains(string(call.args), `"stale:awaited"`) {
						return
					}
				}
				if time.Now().After(deadline) {
					t.Fatal("the awaited ui.notify never reached the host")
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
