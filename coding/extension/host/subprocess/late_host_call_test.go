package subprocess

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pi's ExtensionRunner.emit (runner.ts:988-1017) awaits the handler, not work
// it schedules independently. pi-powerline-footer opens its welcome overlay
// from a timer after session_start returns. A gate makes the same inherited
// async context outlive its request deterministically, without a timer race.
func TestNodeUICallScheduledByAFinishedHandlerRuns(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	entry := filepath.Join(t.TempDir(), "late-overlay.ts")
	write(t, entry, `export default function (pi: any) {
	let release: () => void;
	pi.registerCommand("late", {
		handler: (_args: string, ctx: any) => {
			const gate = new Promise<void>((resolve) => { release = resolve; });
			void gate.then(() => ctx.ui.custom((_tui: any, _theme: any, _kb: any, done: (value: string) => void) => ({
				render: () => ["late-overlay"],
				invalidate() {},
				handleInput: () => done("closed"),
			}), { overlay: true }));
		},
	});
	pi.registerCommand("release", { handler: () => release() });
}
`)
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			fakeUI := newTestUIContext()
			bridge := NewUIBridge(func() {})
			bridge.SetUIContext(fakeUI)
			bridge.SetWidth(80)
			bridge.SetActions(&HostCallbacks{IsIdle: func() bool { return true }})
			h := newTestHost(t)
			h.SetWidthFunc(func() int { return 80 })
			h.SetUIBridge(bridge)
			t.Cleanup(func() { h.Shutdown("test done") })
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			exts, errs := h.LoadAll(ctx, []ExtConfig{{Name: "late", Source: entry, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 || len(exts) != 1 {
				t.Fatalf("LoadAll = %v, %v", exts, errs)
			}
			for _, command := range []string{"late", "release"} {
				if err := exts[0].Commands[command].Handler(ctx, ""); err != nil {
					t.Fatalf("command %s: %v", command, err)
				}
			}
			handle, host := fakeUI.awaitOverlay(t, testTimeout(t, 10*time.Second))
			pollUntil(t, testTimeout(t, 10*time.Second), "the late overlay never rendered", func() bool {
				lines := handle.Lines()
				return len(lines) > 0 && strings.Contains(lines[0], "late-overlay")
			})
			host.OnInput("x")
			select {
			case <-handle.Done():
			case <-time.After(testTimeout(t, 10*time.Second)):
				t.Fatal("the late overlay did not close on input")
			}
		})
	}
}
