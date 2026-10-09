//go:build !pig_strip_node_extensions

package subprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Pi runs an extension's host calls in-process: ctx.ui.custom returns a
// promise that resolves with the component's done() value and nothing ties it
// to the command that started it (.upstream/v0.87.1/packages/coding-agent/src/
// modes/interactive/interactive-mode.ts showExtensionCustom 2858-2940, whose
// rejection path ignores a failure once done ran, `if (closed) return` 2930;
// core/extensions/runner.ts wrapUIPromptContext 525-536). A command that returns while its custom call is
// unsettled, or drops the promise, therefore neither rejects that promise nor
// ends the process. pi-mcp-adapter's /mcp-adapter closes its panel with
// done(undefined) then resolve(), and drops the ui.custom promise
// (commands.ts:760-771); public issue #103.
func TestNodeCommandReturnDoesNotCancelItsHostCalls(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	settledFile := filepath.Join(dir, "settled")
	entry := filepath.Join(dir, "dropped-call.ts")
	write(t, entry, fmt.Sprintf(`import * as fs from "node:fs";
const factory = (settle: () => void) => (_tui: any, _theme: any, _kb: any, done: (value: string) => void) => ({
	render: () => ["dropped-call-overlay"],
	invalidate() {},
	handleInput: () => { done("closed"); settle(); },
});
export default function (pi: any) {
	// pi-mcp-adapter: the command resolves in the same turn as done(); the promise ctx.ui.custom returns is dropped.
	pi.registerCommand("panel", {
		handler: async (_args: string, ctx: any) => {
			await new Promise<void>((resolve) => {
				ctx.ui.custom(factory(resolve), { overlay: true });
			});
		},
	});
	// The command returns at once; the overlay stays open and its promise settles later, as in Pi.
	pi.registerCommand("leave", {
		handler: (_args: string, ctx: any) => {
			ctx.ui.custom(factory(() => {}), { overlay: true }).then((value: string) => fs.writeFileSync(%q, value));
		},
	});
	pi.registerCommand("pid", { handler: () => fs.writeFileSync(%q, String(process.pid)) });
}
`, settledFile, pidFile))
	readPID := func(t *testing.T) string {
		t.Helper()
		data, err := os.ReadFile(pidFile)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, isolation := range []string{"", "isolated"} {
		for _, command := range []string{"panel", "leave"} {
			t.Run(fmt.Sprintf("isolation=%s/%s", isolation, command), func(t *testing.T) {
				_ = os.Remove(settledFile)
				fakeUI := newTestUIContext()
				bridge := NewUIBridge(func() {})
				bridge.SetUIContext(fakeUI)
				bridge.SetWidth(80)
				bridge.SetActions(&HostCallbacks{IsIdle: func() bool { return true }})
				h := newTestHost(t)
				h.SetWidthFunc(func() int { return 80 })
				h.SetUIBridge(bridge)
				var noticeMu sync.Mutex
				var notices []string
				h.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
					noticeMu.Lock()
					defer noticeMu.Unlock()
					notices = append(notices, FormatCrashNotice(name, delay, disabled, reason))
				})
				t.Cleanup(func() { h.Shutdown("test done") })
				ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
				defer cancel()
				exts, errs := h.LoadAll(ctx, []ExtConfig{{Name: "dropped-call", Source: entry, Enabled: true, Isolation: isolation}})
				if len(errs) != 0 || len(exts) != 1 {
					t.Fatalf("LoadAll = %v, %v", exts, errs)
				}
				if err := exts[0].Commands["pid"].Handler(ctx, ""); err != nil {
					t.Fatal(err)
				}
				before := readPID(t)

				returned := make(chan error, 1)
				go func() { returned <- exts[0].Commands[command].Handler(ctx, "") }()
				handle, host := fakeUI.awaitOverlay(t, testTimeout(t, 10*time.Second))
				pollUntil(t, testTimeout(t, 10*time.Second), "the overlay never rendered", func() bool {
					lines := handle.Lines()
					return len(lines) > 0 && strings.Contains(lines[0], "dropped-call-overlay")
				})
				if command == "leave" {
					// The command has returned, and the overlay it opened is still up.
					if err := <-returned; err != nil {
						t.Fatalf("command %s: %v", command, err)
					}
					select {
					case <-handle.Done():
						t.Fatal("the command's return closed the overlay it left open")
					case <-time.After(200 * time.Millisecond):
					}
				}
				host.OnInput("x")
				select {
				case <-handle.Done():
				case <-time.After(testTimeout(t, 10*time.Second)):
					t.Fatal("the overlay did not close on input")
				}
				if command == "panel" {
					if err := <-returned; err != nil {
						t.Fatalf("command %s: %v", command, err)
					}
				} else {
					pollUntil(t, testTimeout(t, 10*time.Second), "the dropped promise never resolved with done's value", func() bool {
						data, _ := os.ReadFile(settledFile)
						return string(data) == "closed"
					})
				}

				// The process must survive: a crash, quarantine or restart shows as a notice or a new pid.
				time.Sleep(500 * time.Millisecond)
				if err := exts[0].Commands["pid"].Handler(ctx, ""); err != nil {
					t.Fatalf("pid after %s: %v", command, err)
				}
				if after := readPID(t); after != before {
					t.Fatalf("extension process restarted after %s: pid %s -> %s", command, before, after)
				}
				noticeMu.Lock()
				defer noticeMu.Unlock()
				if len(notices) != 0 {
					t.Fatalf("crash notices after %s: %q", command, notices)
				}
			})
		}
	}
}

// Cancelling a command rejects the host calls it awaits so it can unwind (pig
// additive D19). A call the extension dropped is no one's to observe: Pi
// never rejects it (interactive-mode.ts showExtensionCustom 2858-2940), so
// the rejection the host raises must not surface as an unhandled rejection
// that ends the process.
func TestNodeCancelledCommandDoesNotCrashOnDroppedHostCall(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	entry := filepath.Join(dir, "cancelled-drop.ts")
	write(t, entry, fmt.Sprintf(`import * as fs from "node:fs";
export default function (pi: any) {
	pi.registerCommand("hold", {
		handler: async (_args: string, ctx: any) => {
			ctx.ui.custom((_tui: any, _theme: any, _kb: any, _done: (value: string) => void) => ({
				render: () => ["cancelled-drop-overlay"],
				invalidate() {},
			}), { overlay: true });
			await new Promise<void>(() => {});
		},
	});
	pi.registerCommand("pid", { handler: () => fs.writeFileSync(%q, String(process.pid)) });
}
`, pidFile))
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
			var noticeMu sync.Mutex
			var notices []string
			h.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
				noticeMu.Lock()
				defer noticeMu.Unlock()
				notices = append(notices, FormatCrashNotice(name, delay, disabled, reason))
			})
			t.Cleanup(func() { h.Shutdown("test done") })
			ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
			defer cancel()
			exts, errs := h.LoadAll(ctx, []ExtConfig{{Name: "cancelled-drop", Source: entry, Enabled: true, Isolation: isolation}})
			if len(errs) != 0 || len(exts) != 1 {
				t.Fatalf("LoadAll = %v, %v", exts, errs)
			}
			if err := exts[0].Commands["pid"].Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}

			commandCtx, cancelCommand := context.WithCancel(ctx)
			returned := make(chan error, 1)
			go func() { returned <- exts[0].Commands["hold"].Handler(commandCtx, "") }()
			handle, _ := fakeUI.awaitOverlay(t, testTimeout(t, 10*time.Second))
			pollUntil(t, testTimeout(t, 10*time.Second), "the overlay never rendered", func() bool {
				lines := handle.Lines()
				return len(lines) > 0 && strings.Contains(lines[0], "cancelled-drop-overlay")
			})
			cancelCommand()
			select {
			case <-returned:
			case <-time.After(testTimeout(t, 10*time.Second)):
				t.Fatal("the cancelled command did not return")
			}

			time.Sleep(500 * time.Millisecond)
			if err := exts[0].Commands["pid"].Handler(ctx, ""); err != nil {
				t.Fatalf("pid after cancel: %v", err)
			}
			if after, _ := os.ReadFile(pidFile); string(after) != string(before) {
				t.Fatalf("extension process restarted after cancel: pid %s -> %s", before, after)
			}
			noticeMu.Lock()
			defer noticeMu.Unlock()
			if len(notices) != 0 {
				t.Fatalf("crash notices after cancel: %q", notices)
			}
		})
	}
}

// A reload closes the old generation's connection while the packed Node process keeps running for its successor. A host call the extension
// made on its own and dropped, still pending then, is no one's to observe: Pi's reload never rejects it (interactive-mode.ts
// showExtensionCustom 2858-2940), so the connection's closing must not end the process the successor shares.
func TestNodeReloadDoesNotCrashOnDroppedHostCall(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	entry := filepath.Join(dir, "reload-drop.ts")
	write(t, entry, fmt.Sprintf(`import * as fs from "node:fs";
export default function (pi: any) {
	// pi-powerline-footer: a timer the handler started opens an overlay whose promise nothing awaits.
	pi.registerCommand("leave", {
		handler: (_args: string, ctx: any) => {
			setTimeout(() => {
				ctx.ui.custom((_tui: any, _theme: any, _kb: any, _done: (value: string) => void) => ({
					render: () => ["reload-drop-overlay"],
					invalidate() {},
				}), { overlay: true }).then(() => {});
			}, 0);
		},
	});
	pi.registerCommand("pid", { handler: () => fs.writeFileSync(%q, String(process.pid)) });
}
`, pidFile))
	fakeUI := newTestUIContext()
	bridge := NewUIBridge(func() {})
	bridge.SetUIContext(fakeUI)
	bridge.SetWidth(80)
	bridge.SetActions(&HostCallbacks{IsIdle: func() bool { return true }})
	h := newTestHost(t)
	h.SetWidthFunc(func() int { return 80 })
	h.SetUIBridge(bridge)
	configs := []ExtConfig{{Name: "reload-drop", Source: entry, Enabled: true}}
	h.SetConfigLoader(func() ([]ExtConfig, error) { return configs, nil })
	var noticeMu sync.Mutex
	var notices []string
	h.SetCrashHandler(func(name string, delay time.Duration, disabled bool, reason string) {
		noticeMu.Lock()
		defer noticeMu.Unlock()
		notices = append(notices, FormatCrashNotice(name, delay, disabled, reason))
	})
	t.Cleanup(func() { h.Shutdown("test done") })
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	exts, err := h.Reload(ctx)
	if err != nil || len(exts) != 1 {
		t.Fatalf("Reload = %v, %v", exts, err)
	}
	if err := exts[0].Commands["pid"].Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := exts[0].Commands["leave"].Handler(ctx, ""); err != nil {
		t.Fatal(err)
	}
	handle, _ := fakeUI.awaitOverlay(t, testTimeout(t, 10*time.Second))
	pollUntil(t, testTimeout(t, 10*time.Second), "the overlay never rendered", func() bool {
		lines := handle.Lines()
		return len(lines) > 0 && strings.Contains(lines[0], "reload-drop-overlay")
	})

	exts, err = h.Reload(ctx)
	if err != nil || len(exts) != 1 {
		t.Fatalf("second Reload = %v, %v", exts, err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := exts[0].Commands["pid"].Handler(ctx, ""); err != nil {
		t.Fatalf("pid after reload: %v", err)
	}
	if after, _ := os.ReadFile(pidFile); string(after) != string(before) {
		t.Fatalf("the reload's successor lost its process: pid %s -> %s", before, after)
	}
	noticeMu.Lock()
	defer noticeMu.Unlock()
	if len(notices) != 0 {
		t.Fatalf("crash notices after reload: %q", notices)
	}
}
