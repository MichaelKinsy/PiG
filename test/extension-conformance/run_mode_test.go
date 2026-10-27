package extensionconformance

import (
	"strings"
	"testing"
)

// Pi tells an extension the run mode it is in through ctx.mode: "tui" in interactive mode, "rpc" in RPC mode, "json" for --mode json and "print" for
// print mode (packages/coding-agent/src/modes/interactive/interactive-mode.ts, rpc/rpc-mode.ts, print-mode.ts bindExtensions mode; core/extensions/types.ts ExtensionMode).
// An SDK reads the mode from the ready payload of the host that loaded it, and falls back to "print" when the host sent none (runner.ts: mode = "print"). A host
// that never sent the mode therefore made every SDK report "print" in the TUI, so an extension that guards terminal-only UI on ctx.mode === "tui" did nothing.
// The context-probe command of every SDK cell reports ctx.mode; the row runs each cell, isolated, packed and fused, in every mode. "print" is the SDK fallback, so
// tui, rpc and json are the values that fail when an SDK, or the host for that SDK's cell, reads the fallback; the print cell pins that no other value leaks.
func TestConformance_RunModeReachesEverySDK(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping conformance suite in short mode (builds subprocess fixtures)")
	}
	t.Cleanup(func() { rpcRowMode = "" })
	for _, mode := range []string{"tui", "rpc", "json", "print"} {
		rpcRowMode = mode
		for _, tc := range componentKitSDKCells() {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				h := startKitCell(t, tc)
				*h.notify = nil
				runKitCommand(t, h, "context-probe", "")
				waitFor(t, func() bool { return len(*h.notify) > 0 })
				got := (*h.notify)[0]
				if want := "mode=" + mode + " "; !strings.HasPrefix(got, want) {
					t.Fatalf("%s: context probe = %q, want it to start with %q", tc.name, got, want)
				}
			})
		}
	}
}
