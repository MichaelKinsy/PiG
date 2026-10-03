package subprocess

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// ctx.ui.theme is the host's active theme. Pi's Theme.fg closes a faint token with SGR 22;39 (theme.ts:363) and
// getThinkingBorderColor draws through fg (theme.ts:getThinkingBorderColor), so the faint attribute does not leak
// past the text into the cells a Text component pads after it (parity 24-tool-renderers-collapsed: a tool card
// line drawn with theme.fg("muted") must end its faint run at the text). The host sends each foreground as Pi's
// getFgAnsi opening, which ends in SGR 2 for a faint token (theme.ts:399-402).
func TestNodeUIThemeFgClosesFaintTokensLikePi(t *testing.T) {
	nodeCellRequireNode(t)
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
const {Runtime} = await import(pathToFileURL(%q));
const {Theme} = await import(new URL("./shims/pi-dist/pi-coding-agent/modes/interactive/theme/theme.js", pathToFileURL(%q)));
for (const mode of ["truecolor", "256color"]) {
  const pi = new Theme({accent: "#5f87ff", dim: "#808080", muted: 244, text: "", thinkingXhigh: "#d183e8", thinkingOff: "#444444", bashMode: "#00aa00"}, {selectedBg: "#303030"}, mode, {name: "faint", dim: ["dim", "muted", "thinkingXhigh"]});
  const foregrounds = Object.fromEntries([...pi.fgAnsi.keys()].map((token) => [token, pi.getFgAnsi(token)]));
  const backgrounds = Object.fromEntries([...pi.bgAnsi.keys()].map((token) => [token, pi.getBgAnsi(token)]));
  const r = new Runtime("/ext/theme.mjs");
  r.ui.theme.setPalette({name: "faint", foregrounds, backgrounds, modifiers: true, mode});
  for (const token of Object.keys(foregrounds)) {
    assert.equal(r.ui.theme.fg(token, "x"), pi.fg(token, "x"), mode + " fg " + token);
  }
  for (const level of ["off", "xhigh"]) {
    assert.equal(r.ui.theme.getThinkingBorderColor(level)("x"), pi.getThinkingBorderColor(level)("x"), mode + " thinking " + level);
  }
  assert.equal(r.ui.theme.fg("muted", "x"), pi.getFgAnsi("muted").slice(0, -4) + "\x1b[2mx\x1b[22;39m", mode);
}
`, path, path)
	if out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("faint ui theme: %v\n%s", err, out)
	}
}
