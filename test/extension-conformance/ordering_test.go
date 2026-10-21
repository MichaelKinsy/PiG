package extensionconformance

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
)

// The ordering rows: Pi runs the extensions, the agent loop and its listeners on one thread, so the order of two things that happen "at
// once" in a parallel tool batch is a language fact. A native SDK runs each request on a thread of its own, and the host runs each request's
// work on a goroutine, so each place that touches the boundary must restate the order. Every row sends a batch of order_probe calls through
// the production Session (the agent's parallel dispatch, the Session's tool_result hook, its listeners) and compares what the host or the
// extension did with Pi's order. The expected order is data: a row names the frames that carry it, and the check reads both from the same
// round, so a realization that differs from the others fails, and one that is always wrong in the same way cannot pass by agreeing with a
// constant.
//
// Realizations: every subprocess SDK (isolated Go, Node, Node packed, Rust, Python), fused Go and the packed Go, Rust and Python cells. The
// in-process reference has no wire, and its calls and hooks run inline on the agent's goroutines.
//
// Named mutants, each of which turns the rows red (the red output is in docs/plan/progress or the commit that added the row):
//   - host-calls-apply-in-wire-order: Conn.callLane returns the parent request id for a tool_call request, so each tool's calls run on a lane of
//     its own again (coding/extension/host/subprocess/conn.go), or callLanes.drain skips `next.place.Wait()` (host_calls.go).
//   - tool-result-hooks-start-in-settle-order: loopHost.finalizeCalledTool skips `slot.Begin()` (agent/tool_execution.go).
//   - tool-result-handlers-start-in-request-order: the SDK dispatcher reserves no place for event requests (Python _starts_in_arrival_order
//     without "event"; Go startsInArrivalOrder without "event"; Rust starts_in_arrival_order without "event").
//   - tool-updates-reach-the-session-in-frame-order: Conn.requestWithUpdates runs each request's updates on a lane of its own again.
const (
	orderingBatch  = 8
	orderingRounds = 12
)

type orderingRound struct {
	frames orderFrames
	// applied is the order_probe host calls the host applied (the two ui.notify calls each tool makes), in the order it applied them.
	applied []string
	// updates is the tool call ids of the tool_execution_update events the Session delivered to its listeners, in delivery order.
	updates []string
	// calls is the numbers 0..orderingBatch-1 as strings: the batch's call order.
	calls []string
	// toolCalls is the tool call ids in call order.
	toolCalls []string
}

type orderingRow struct {
	name string
	// upstream cites Pi's order.
	upstream string
	// applies reports whether the realization can show this row; a realization that cannot is skipped, never passed.
	applies func(realization string) bool
	// length is how many entries the reference order holds, orderingBatch when zero: a row whose reference is short did not see the batch reach the wire.
	length int
	// observe returns what happened and Pi's order, as read from the round.
	observe func(orderingRound) (got, want []string)
}

func everyRealization(string) bool { return true }

// firstStatementOrder reports whether the realization's extension runs in one thread, so its handlers' first statements run in the order Pi's one
// thread gives them. A Go, Rust or Python handler runs on a thread of its own, and a thread that the operating system preempts between its hand-off
// and its first statement can be overtaken (D187), so a row about first statements cannot pin those SDKs; the Go and Python SDK tests and the
// Rust places pin their invocation order.
func firstStatementOrder(name string) bool {
	return name == "subprocess-node" || name == "subprocess-node-packed"
}

var orderingRows = []orderingRow{
	{
		name:     "host-calls-apply-in-wire-order",
		upstream: "agent-loop.ts:646-648 (Promise.all(map) runs each call's synchronous prefix in call order on one thread), agent-session.ts:3436-3442 (appendEntry applies synchronously), rpc-mode.ts:152-160 (notify writes synchronously)",
		applies:  everyRealization,
		length:   2 * orderingBatch,
		observe: func(r orderingRound) ([]string, []string) {
			return r.applied, r.frames.notifies("order-call=", "order-then=")
		},
	},
	{
		name:     "host-calls-follow-call-order",
		upstream: "agent-loop.ts:646-648: the tools' handlers start in call order, so their first calls reach the host in call order",
		applies:  firstStatementOrder,
		observe: func(r orderingRound) ([]string, []string) {
			return r.frames.notifies("order-call="), r.calls
		},
	},
	{
		name:     "tool-result-hooks-start-in-settle-order",
		upstream: "agent-loop.ts:619-660 (each call continues from its own execute promise, so continuations of tools that settle back to back run in settle order), agent-session.ts:685-703 (afterToolCall emits tool_result)",
		applies:  everyRealization,
		observe: func(r orderingRound) ([]string, []string) {
			return r.frames.tools("hook"), r.frames.tools("answer")
		},
	},
	{
		name:     "tool-result-handlers-start-in-request-order",
		upstream: "runner.ts:1096-1125: concurrent emits start their handlers in call order on one thread",
		applies:  firstStatementOrder,
		observe: func(r orderingRound) ([]string, []string) {
			return r.frames.notifies("order-result="), r.frames.tools("hook")
		},
	},
	{
		name:     "tool-updates-reach-the-session-in-frame-order",
		upstream: "agent-loop.ts:836-840 (onUpdate emits at call time, not awaited, so updates reach the listeners in the order the tools called onUpdate)",
		applies:  everyRealization,
		observe: func(r orderingRound) ([]string, []string) {
			return r.updates, r.frames.tools("update")
		},
	},
}

func orderingCases() []harnessCase {
	return append(sdkHarnessCases(), packedSDKHarnessCases()...)
}

// runOrderingRound runs one batch of order_probe calls through a Session over the harness's runner and returns what the round did.
func runOrderingRound(t *testing.T, h *harness, session *coding.Session, provider *ai.FauxProviderHandle, round int) orderingRound {
	t.Helper()
	before := len(h.ui.Recorded())
	h.wire.on.Store(true)
	h.wire.order.take()
	var mu sync.Mutex
	var updates []string
	unsubscribe := session.Subscribe(func(event agent.AgentEvent) {
		if update, ok := event.(agent.ToolExecutionUpdateEvent); ok && update.ToolName == "order_probe" {
			mu.Lock()
			updates = append(updates, update.ToolCallID)
			mu.Unlock()
		}
	})
	defer unsubscribe()
	calls := make([]ai.FauxContentBlock, orderingBatch)
	r := orderingRound{}
	for i := range orderingBatch {
		id := fmt.Sprintf("order-%d-%d", round, i)
		calls[i] = ai.FauxToolCall("order_probe", map[string]any{"n": i}, &ai.FauxToolCallOptions{ID: id})
		r.calls = append(r.calls, fmt.Sprint(i))
		r.toolCalls = append(r.toolCalls, id)
	}
	provider.SetResponses([]ai.FauxResponseStep{
		ai.FauxStaticStep(ai.FauxResponse{Content: calls, StopReason: "toolUse"}),
		ai.FauxStaticStep(ai.FauxResponse{Content: []ai.FauxContentBlock{ai.FauxText("done")}, StopReason: "stop"}),
	})
	if _, err := session.Send(t.Context(), "go"); err != nil {
		t.Fatal(err)
	}
	// The calls and the hooks' notifications reach the host after the tools answered; wait for all of them.
	pollUntilConformance(t, "the host did not apply every order_probe call and tool_result hook report", func() bool {
		applied := 0
		for _, entry := range h.ui.Recorded()[before:] {
			if strings.HasPrefix(entry, "order-call=") || strings.HasPrefix(entry, "order-then=") || strings.HasPrefix(entry, "order-result=") {
				applied++
			}
		}
		return applied >= 3*orderingBatch
	})
	for _, entry := range h.ui.Recorded()[before:] {
		if call, ok := strings.CutPrefix(entry, "order-call="); ok {
			r.applied = append(r.applied, strings.TrimSuffix(call, ":info"))
		} else if then, ok := strings.CutPrefix(entry, "order-then="); ok {
			r.applied = append(r.applied, strings.TrimSuffix(then, ":info"))
		}
	}
	mu.Lock()
	r.updates = slices.Clone(updates)
	mu.Unlock()
	h.wire.on.Store(false)
	r.frames = h.wire.order.take()
	return r
}

func TestOrderingRowsAcrossSDKs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	for _, tc := range orderingCases() {
		t.Run(tc.name, func(t *testing.T) {
			h := tc.make(t)
			h.wire.order.ext = tc.extName
			t.Cleanup(func() {
				if h.cleanup != nil {
					h.cleanup()
				}
				if h.host != nil {
					h.host.Shutdown("test done")
				}
			})
			services, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: t.TempDir(), AgentDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			tools, errs := coding.BridgeNewRunnerTools(h.runner.Tools())
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			provider := ai.NewFauxProvider(ai.FauxConfig{})
			session, err := coding.NewSession(services, coding.SessionOptions{Model: &ai.Model{ID: "faux-1", Provider: provider}, Runner: h.runner, Tools: tools, SkipBuiltinTools: true, NoSession: true})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() {
				defer close(done)
				for range session.Events() {
				}
			}()
			t.Cleanup(func() { _ = session.Close(); <-done })
			for round := range orderingRounds {
				r := runOrderingRound(t, h, session, provider, round)
				for _, row := range orderingRows {
					if !row.applies(tc.name) {
						continue
					}
					got, want := row.observe(r)
					length := cmp.Or(row.length, orderingBatch)
					if len(want) != length {
						t.Errorf("row %s, round %d: the reference order has %d entries, want %d (the batch did not reach the wire): %v", row.name, round, len(want), length, want)
						continue
					}
					if !slices.Equal(got, want) {
						t.Errorf("row %s, round %d: got %v, want %v\n  Pi: %s", row.name, round, got, want, row.upstream)
					}
				}
			}
		})
	}
}
