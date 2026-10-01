package subprocess

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The runtime forgives only its own cancellation of a dropped host call. An
// extension's own unhandled rejection stays fatal to its process, as an
// unhandled rejection is to Pi's.
func TestNodeExtensionUnhandledRejectionStillCrashes(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	dir := t.TempDir()
	entry := filepath.Join(dir, "unhandled.ts")
	write(t, entry, `export default function (pi: any) {
	pi.registerCommand("bug", { handler: () => { setTimeout(() => { Promise.reject(new Error("extension bug")); }, 0); } });
	pi.registerCommand("string-bug", { handler: () => { setTimeout(() => { Promise.reject("extension bug"); }, 0); } });
}
`)
	for _, isolation := range []string{"", "isolated"} {
		for _, command := range []string{"bug", "string-bug"} {
			t.Run(fmt.Sprintf("isolation=%s/%s", isolation, command), func(t *testing.T) {
				h := newTestHost(t)
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
				exts, errs := h.LoadAll(ctx, []ExtConfig{{Name: "unhandled", Source: entry, Enabled: true, Isolation: isolation}})
				if len(errs) != 0 || len(exts) != 1 {
					t.Fatalf("LoadAll = %v, %v", exts, errs)
				}
				// The rejection can end the process before the command's response arrives, so the handler's own outcome is not part of the contract.
				_ = exts[0].Commands[command].Handler(ctx, "")
				pollUntil(t, testTimeout(t, 10*time.Second), "the extension's unhandled rejection did not end its process", func() bool {
					noticeMu.Lock()
					defer noticeMu.Unlock()
					return len(notices) > 0
				})
			})
		}
	}
}

// An extension's own unhandledRejection listener handles its rejections, as Node emits the event to every listener and raises only when none
// is registered. In Pi the extension shares Pi's process, so its listener keeps Pi alive; the runtime's own listener must not raise past it.
func TestNodeExtensionUnhandledRejectionListenerKeepsProcess(t *testing.T) {
	nodeCellRequireNode(t)
	shortSockDir(t)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	handledFile := filepath.Join(dir, "handled")
	entry := filepath.Join(dir, "listener.ts")
	write(t, entry, fmt.Sprintf(`import * as fs from "node:fs";
export default function (pi: any) {
	process.on("unhandledRejection", (reason: any) => { fs.writeFileSync(%q, String(reason?.message ?? reason)); });
	pi.registerCommand("bug", { handler: () => { setTimeout(() => { Promise.reject(new Error("extension bug")); }, 0); } });
	pi.registerCommand("pid", { handler: () => fs.writeFileSync(%q, String(process.pid)) });
}
`, handledFile, pidFile))
	for _, isolation := range []string{"", "isolated"} {
		t.Run("isolation="+isolation, func(t *testing.T) {
			_ = os.Remove(handledFile)
			h := newTestHost(t)
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
			exts, errs := h.LoadAll(ctx, []ExtConfig{{Name: "listener", Source: entry, Enabled: true, Isolation: isolation}})
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
			if err := exts[0].Commands["bug"].Handler(ctx, ""); err != nil {
				t.Fatal(err)
			}
			pollUntil(t, testTimeout(t, 10*time.Second), "the extension's listener never saw its rejection", func() bool {
				data, _ := os.ReadFile(handledFile)
				return string(data) == "extension bug"
			})
			time.Sleep(500 * time.Millisecond)
			if err := exts[0].Commands["pid"].Handler(ctx, ""); err != nil {
				t.Fatalf("pid after a handled rejection: %v", err)
			}
			if after, _ := os.ReadFile(pidFile); string(after) != string(before) {
				t.Fatalf("extension process restarted after a handled rejection: pid %s -> %s", before, after)
			}
			noticeMu.Lock()
			defer noticeMu.Unlock()
			if len(notices) != 0 {
				t.Fatalf("crash notices after a handled rejection: %q", notices)
			}
		})
	}
}
