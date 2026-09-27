package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// Every state snapshot carries the host terminal's resolved capabilities.
func TestStateSnapshotCarriesTerminalCapabilities(t *testing.T) {
	bridge := NewUIBridge(func() {})
	if got := bridge.Snapshot(nil, 0, false).TerminalCapabilities; got != nil {
		t.Fatalf("no capability source: snapshot carries %+v", got)
	}
	want := TerminalCapabilitiesPayload{Images: "kitty", TrueColor: true, Hyperlinks: true}
	bridge.SetTerminalCapabilitiesFunc(func() TerminalCapabilitiesPayload { return want })
	if got := bridge.Snapshot(nil, 0, false).TerminalCapabilities; got == nil || *got != want {
		t.Fatalf("snapshot capabilities = %+v, want %+v", got, want)
	}
}

// Pi's Markdown renders links as OSC 8 hyperlinks only when the terminal
// supports them. The runtime seeds pi-tui's capability cache from the host's
// state snapshots, so an extension's Markdown follows the host terminal.
func TestNodeStateSeedsTerminalCapabilitiesForMarkdown(t *testing.T) {
	abs := func(rel string) string {
		path, err := filepath.Abs(rel)
		if err != nil {
			t.Fatal(err)
		}
		return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import { Runtime } from %q;
import { Markdown } from %q;
const theme = new Proxy({}, { get: () => (s) => s });
const runtime = new Runtime("/ext/caps.mjs");
const render = () => new Markdown("see [docs](https://example.com)", 0, 0, theme).render(40)[0].trimEnd();
runtime.applyState({ terminalCapabilities: { trueColor: true, hyperlinks: false } });
assert.equal(render(), "see docs (https://example.com)");
runtime.applyState({ terminalCapabilities: { images: "kitty", trueColor: true, hyperlinks: true } });
assert.equal(render(), "see \x1b]8;;https://example.com\x1b\\docs\x1b]8;;\x1b\\");
`, abs("runtime-node/runtime.mjs"), abs("runtime-node/shims/pi-tui.mjs"))
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("terminal capabilities for Markdown: %v\n%s", err, output)
	}
}
