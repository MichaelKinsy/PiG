package subprocess

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's showExtensionCustom hands the factory its keybindings manager and
// focuses the component it shows (unless the overlay is non-capturing), so a
// panel resolves tui.select.* through the manager (pi-mcp-adapter's
// createPanelKeys) and a focused Input renders the cursor marker.
func TestNodeCustomFactoryGetsKeybindingsAndFocus(t *testing.T) {
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
import { CURSOR_MARKER, Input, KeybindingsManager, getKeybindings } from %q;
for (const [name, options, focused] of [
  ["inline", undefined, true],
  ["overlay", {overlay: true}, true],
  ["non-capturing overlay", {overlay: true, overlayOptions: {nonCapturing: true}}, false],
]) {
  const runtime = new Runtime("/ext/keys.mjs");
  runtime.ready = {width: 80, height: 24};
  let release;
  const frames = [];
  runtime.call = () => new Promise(resolve => { release = resolve; });
  runtime.notify = (method, args) => { if (method === "ui.custom.render") frames.push(args); };
  let keybindings;
  const input = new Input();
  const pending = runtime.openCustomOverlay((_tui, _theme, kb) => { keybindings = kb; return input; }, options);
  for (let i = 0; i < 10 && !release; i++) await Promise.resolve();
  try {
    assert.ok(keybindings instanceof KeybindingsManager, name);
    assert.equal(keybindings, getKeybindings(), name);
    assert.ok(keybindings.matches("\x1b[A", "tui.select.up"), name);
    assert.ok(keybindings.matches("\r", "tui.select.confirm"), name);
    assert.equal(input.focused, focused, name);
    assert.equal(frames[0]?.lines[0].includes(CURSOR_MARKER), focused, name);
  } finally { release({}); await pending; }
}
`, abs("runtime-node/runtime.mjs"), abs("runtime-node/shims/pi-tui.mjs"))
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("custom factory keybindings and focus: %v\n%s", err, output)
	}
}
