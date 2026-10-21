//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
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
			turns.Ticked()
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
