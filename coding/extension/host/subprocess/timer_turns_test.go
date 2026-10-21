//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/jsloop"
)

// notifyRecorder records the notifications the host receives, in order.
type notifyRecorder struct {
	*testUIContext
	mu  sync.Mutex
	got []string
}

func (r *notifyRecorder) Notify(message, _ string) {
	r.mu.Lock()
	r.got = append(r.got, message)
	r.mu.Unlock()
}

func (r *notifyRecorder) notices() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

// pig additive (D189): Pi runs a setImmediate or setTimeout callback an extension queued only when the agent's run awaits real work or is over, so its ui.notify reaches the UI behind everything the run's microtasks write. A Node runtime marks the host calls of such a callback that fires after its request's handler returned, and the host holds them while a chain runs (jsloop.Turns).
// Probed against Pi 1.3.0: a tool's synchronous, queueMicrotask and promise notifications precede tool_execution_end; setImmediate and setTimeout(0) notifications follow agent_settled (test/parity scenario 63).
func TestNodeTimerCallbackCallsWaitForTheLoopTurn(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	entry := filepath.Join(t.TempDir(), "timer-calls.ts")
	write(t, entry, `export default function (pi: any) {
	pi.registerCommand("defer", {
		handler: (_args: string, ctx: any) => {
			process.nextTick(() => ctx.ui.notify("tick", "info"));
			setImmediate(() => ctx.ui.notify("immediate", "info"));
			setTimeout(() => ctx.ui.notify("timeout", "info"), 0);
			queueMicrotask(() => ctx.ui.notify("microtask", "info"));
			Promise.resolve().then(() => ctx.ui.notify("promise", "info"));
			ctx.ui.notify("sync", "info");
		},
	});
}
`)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			ui := &notifyRecorder{testUIContext: newTestUIContext()}
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(ui)
			bridge.SetWidth(80)
			bridge.SetActions(&HostCallbacks{IsIdle: func() bool { return true }})
			h := newTestHost(t)
			h.SetUIBridge(bridge)
			var turns jsloop.Turns
			h.SetTimerTurns(&turns)
			t.Cleanup(func() { h.Shutdown("test done") })
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			exts, errs := h.LoadAll(ctx, []ExtConfig{{Name: "timer-calls", Source: entry, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 || len(exts) != 1 {
				t.Fatalf("LoadAll = %v, %v", exts, errs)
			}
			end := turns.Chain()
			if err := exts[0].Commands["defer"].Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			// Both callbacks' calls reach the host and wait: nothing else is left to arrive, and the continuations of the handler wrote theirs.
			pollUntil(t, testTimeout(t, 10*time.Second), "the callbacks' calls never reached the host", func() bool { return turns.Held() == 3 })
			if got, want := ui.notices(), []string{"sync", "microtask", "promise"}; !slices.Equal(got, want) {
				t.Fatalf("before the loop turns the UI got %q, want %q", got, want)
			}
			// Pi runs the nextTick callback right after the tool_execution_end of the tool that queued it, before the timers.
			turns.Ticked("")
			if got, want := ui.notices(), []string{"sync", "microtask", "promise", "tick"}; !slices.Equal(got, want) {
				t.Fatalf("after the tick boundary the UI got %q, want %q", got, want)
			}
			end()
			pollUntil(t, testTimeout(t, 10*time.Second), "the held calls never ran", func() bool { return len(ui.notices()) == 6 })
			if got, want := ui.notices(), []string{"sync", "microtask", "promise", "tick", "immediate", "timeout"}; !slices.Equal(got, want) {
				t.Fatalf("after the chain ended the UI got %q, want %q", got, want)
			}
		})
	}
}

// pig additive (D189): Pi runs the callbacks a tool queued right after that tool's tool_execution_end, while a sibling tool awaits real work and the loop turns. The Node runtime is another process, so a callback's call can reach the host before or after the event; the call names the tool call whose handler queued the callback (CallPayload.OriginToolCallID), and the host runs it behind that call's end whichever came first. A timer queued 100 ms ahead reaches the host long after the end; held for the next end of any tool, it would reach the UI behind the sibling's result.
func TestNodeTickCallsOfAToolRunAfterItsEndWhenTheyArriveLate(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	entry := filepath.Join(t.TempDir(), "tick-origin.ts")
	write(t, entry, `export default function (pi: any) {
	pi.registerTool({
		name: "slow",
		label: "slow",
		description: "Awaits a timer for a long while",
		parameters: { type: "object", properties: {} },
		async execute(_id: string, _params: any, _signal: any, _onUpdate: any, ctx: any) {
			ctx.ui.notify("slow:started", "info");
			await new Promise((resolve) => setTimeout(resolve, 20000));
			return { content: [{ type: "text", text: "slow" }], details: {} };
		},
	});
	pi.registerTool({
		name: "late",
		label: "late",
		description: "Queues an immediate and a timer and returns",
		parameters: { type: "object", properties: {} },
		async execute(_id: string, _params: any, _signal: any, _onUpdate: any, ctx: any) {
			setImmediate(() => ctx.ui.notify("late:immediate", "info"));
			setTimeout(() => ctx.ui.notify("late:timeout", "info"), 100);
			ctx.ui.notify("late:sync", "info");
			return { content: [{ type: "text", text: "late" }], details: {} };
		},
	});
}
`)
	ui := &notifyRecorder{testUIContext: newTestUIContext()}
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(ui)
	bridge.SetWidth(80)
	bridge.SetActions(&HostCallbacks{IsIdle: func() bool { return true }})
	h := newTestHost(t)
	h.SetUIBridge(bridge)
	var turns jsloop.Turns
	h.SetTimerTurns(&turns)
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	exts, errs := h.LoadAll(ctx, []ExtConfig{{Name: "tick-origin", Source: entry, Enabled: true}})
	if len(errs) != 0 || len(exts) != 1 {
		t.Fatalf("LoadAll = %v, %v", exts, errs)
	}
	end := turns.Chain()
	defer end()
	// The slow tool is the sibling that keeps the run awaiting. The host sees no wait in an extension tool's own await: the runtime marks the call tick because a sibling still runs.
	slowCtx, cancelSlow := context.WithCancel(ctx)
	slowDone := make(chan error, 1)
	go func() {
		_, err := exts[0].Tools["slow"].Definition.Execute(slowCtx, "slow-1", json.RawMessage(`{}`), nil)
		slowDone <- err
	}()
	defer func() {
		cancelSlow()
		select {
		case <-slowDone:
		case <-time.After(10 * time.Second):
		}
	}()
	pollUntil(t, testTimeout(t, 10*time.Second), "the slow tool never started", func() bool { return slices.Contains(ui.notices(), "slow:started") })
	if _, err := exts[0].Tools["late"].Definition.Execute(ctx, "late-1", json.RawMessage(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	// The agent writes the tool's tool_execution_end here, long before the timer fires.
	turns.Ticked("late-1")
	pollUntil(t, testTimeout(t, 10*time.Second), "the callbacks of the tool that ended never reached the UI while its sibling still awaited", func() bool { return len(ui.notices()) == 4 })
	if got, want := ui.notices(), []string{"slow:started", "late:sync", "late:immediate", "late:timeout"}; !slices.Equal(got, want) {
		t.Fatalf("the UI got %q, want %q", got, want)
	}
	select {
	case err := <-slowDone:
		t.Fatalf("the slow tool returned (%v) before its sibling's callbacks ran, so the calls were not made while a sibling awaited", err)
	default:
	}
}
