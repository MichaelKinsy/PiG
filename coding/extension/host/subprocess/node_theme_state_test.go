package subprocess

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi interactive-mode.ts:2572-2583 and theme.ts:570-575,790-815 return actual named themes and synchronous selection results.
func TestNodeThemeCallsReturnHostResultsSynchronously(t *testing.T) {
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
const r = new Runtime("/ext/theme.mjs");
r.hostReady = true;
r.applyState({hasUI:true});
let modifiers = true;
const palette = name => ({name, modifiers, foregrounds:{accent:name === "light" ? "\x1b[38;2;1;2;3m" : "\x1b[38;2;4;5;6m"}, backgrounds:{selectedBg:"\x1b[48;2;1;2;3m"},mode:"truecolor"});
let active = "dark";
r.ui.theme.setPalette(palette(active));
r.conn = {requestState(){}, call(){throw new Error("theme operation was detached");}, callSync(method,args) {
 if(method === "ui.getTheme") return {theme:args.name === "light" ? palette("light") : null};
 if(method === "ui.theme") return {theme:palette(active)};
 assert.equal(method,"ui.setTheme");
 assert.deepEqual(Object.keys(args),["theme"]);
 active = args.theme === "light" ? "light" : "dark";
 return args.theme === "light" ? {success:true} : {success:false,error:"Theme not found: " + args.theme};
}};
const light = r.ctx.ui.getTheme("light");
assert.ok(light instanceof Theme, "getTheme returns a Theme, not list metadata");
assert.equal(light.name,"light");
assert.equal(light.fg("accent","x"),"\x1b[38;2;1;2;3mx\x1b[39m");
assert.equal(light.bold("x"),"\x1b[1mx\x1b[22m", "named Theme uses the host's color capability");
modifiers = false;
assert.equal(r.ctx.ui.getTheme("light").bold("x"),"x", "disabled host styles stay disabled");
modifiers = true;
assert.equal(r.ctx.ui.theme.name,"dark", "lookup does not select");
assert.equal(r.ctx.ui.getTheme("missing"),undefined);
assert.deepEqual(r.ctx.ui.setTheme("light"),{success:true});
assert.equal(r.ctx.ui.theme.name,"light", "selection visible before return");
assert.deepEqual(r.ctx.ui.setTheme("missing"),{success:false,error:"Theme not found: missing"});
assert.equal(r.ctx.ui.theme.name,"dark", "failure exposes the host fallback");
`, path, path)
	if out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("theme: %v\n%s", err, out)
	}
}

// A host theme carries each foreground as Pi's getFgAnsi opening, which ends
// in SGR 2 for a faint token (theme.ts:399-402). getTheme rehydrates Pi's
// Theme from that palette, so fg, style and getFgAnsi draw and close a faint
// token as Pi's own Theme does (theme.ts:344,363): the faint attribute is
// closed with SGR 22 and does not leak past the text.
func TestNodeHostThemeKeepsPiFaintTokens(t *testing.T) {
	nodeCellRequireNode(t)
	path, err := filepath.Abs("runtime-node/theme-palette.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
const {themeFromPalette} = await import(pathToFileURL(%q));
const {Theme} = await import(new URL("./shims/pi-dist/pi-coding-agent/modes/interactive/theme/theme.js", pathToFileURL(%q)));
for (const mode of ["truecolor", "256color"]) {
  const pi = new Theme({accent: "#5f87ff", dim: "#808080", muted: 244, text: "", thinkingXhigh: "#d183e8"}, {selectedBg: "#303030", userMessageBg: ""}, mode, {name: "faint", dim: ["dim", "muted"]});
  const foregrounds = Object.fromEntries([...pi.fgAnsi.keys()].map((token) => [token, pi.getFgAnsi(token)]));
  const backgrounds = Object.fromEntries([...pi.bgAnsi.keys()].map((token) => [token, pi.getBgAnsi(token)]));
  const host = themeFromPalette({name: "faint", foregrounds, backgrounds, modifiers: true, mode});
  const scene = (theme) => [
    ...Object.keys(foregrounds).flatMap((token) => [theme.fg(token, "x"), theme.getFgAnsi(token), theme.style("y", {fg: token, bold: true})]),
    ...Object.keys(backgrounds).flatMap((token) => [theme.bg(token, "x"), theme.getBgAnsi(token), theme.style("y", {bg: token})]),
  ];
  assert.deepEqual(scene(host), scene(pi), mode);
  assert.equal(host.fg("dim", "x"), pi.getFgAnsi("dim").slice(0, -4) + "\x1b[2mx\x1b[22;39m", mode);
}
`, path, path)
	if out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("faint host theme: %v\n%s", err, out)
	}
}

// The active host theme (ctx.ui.theme, the theme tool renderers draw with) takes the same palette as getTheme: Pi's
// getFgAnsi opening, which ends in SGR 2 for a faint token (theme.ts:399-402). fg closes a faint token with SGR 22;39
// (theme.ts:363), so the faint attribute does not leak past the text into the padding of an exported line.
func TestNodeActiveThemeKeepsPiFaintTokens(t *testing.T) {
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
  const pi = new Theme({accent: "#5f87ff", dim: "#808080", muted: 244, text: "", thinkingXhigh: "#d183e8", bashMode: "#00ff00"}, {selectedBg: "#303030", userMessageBg: ""}, mode, {name: "faint", dim: ["dim", "muted"]});
  const foregrounds = Object.fromEntries([...pi.fgAnsi.keys()].map((token) => [token, pi.getFgAnsi(token)]));
  const backgrounds = Object.fromEntries([...pi.bgAnsi.keys()].map((token) => [token, pi.getBgAnsi(token)]));
  const r = new Runtime("/ext/theme.mjs");
  r.hostReady = true;
  r.applyState({hasUI: true});
  r.ui.theme.setPalette({name: "faint", foregrounds, backgrounds, modifiers: true, mode});
  const active = r.ctx.ui.theme;
  for (const token of ["accent", "dim", "muted", "thinkingXhigh", "bashMode"]) {
    assert.equal(active.fg(token, "x"), pi.fg(token, "x"), mode + " " + token);
  }
  assert.equal(active.fg("dim", "x"), pi.getFgAnsi("dim") + "x\x1b[22;39m", mode);
  assert.equal(active.getThinkingBorderColor("xhigh")("t"), pi.getThinkingBorderColor("xhigh")("t"), mode);
  assert.equal(active.getBashModeBorderColor()("t"), pi.getBashModeBorderColor()("t"), mode);
}
`, path, path)
	if out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("active faint theme: %v\n%s", err, out)
	}
}

// A host theme answers appearance and colors from the palette the host resolved (theme.ts:306,312-336): Pi's Theme would rebuild them from concrete colors the palette does not carry, and mix a faint token toward the background a second time.
func TestNodeHostThemeCarriesResolvedAppearanceAndColors(t *testing.T) {
	nodeCellRequireNode(t)
	path, err := filepath.Abs("runtime-node/theme-palette.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
const {themeFromPalette} = await import(pathToFileURL(%q));
const {Theme} = await import(new URL("./shims/pi-dist/pi-coding-agent/modes/interactive/theme/theme.js", pathToFileURL(%q)));
const pi = new Theme({accent: "oklch(62%% 0.1 200)", dim: "#808080", muted: 244, text: "", thinkingXhigh: "#d183e8"}, {selectedBg: "#303030", userMessageBg: ""}, "truecolor", {name: "faint", dim: ["dim"], appearance: "light"});
const foregrounds = Object.fromEntries([...pi.fgAnsi.keys()].map((token) => [token, pi.getFgAnsi(token)]));
const backgrounds = Object.fromEntries([...pi.bgAnsi.keys()].map((token) => [token, pi.getBgAnsi(token)]));
const palette = {name: "faint", foregrounds, backgrounds, modifiers: true, mode: "truecolor", appearance: pi.appearance, colors: JSON.parse(JSON.stringify(pi.colors))};
const host = themeFromPalette(palette);
assert.equal(host.appearance, "light");
assert.deepEqual(host.colors, pi.colors);
assert.ok(Object.isFrozen(host.colors));
// A palette without them leaves the theme as before: no colors of its own.
assert.deepEqual(themeFromPalette({...palette, appearance: undefined, colors: undefined}).colors, {});
`, path, path)
	if out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("host theme colors: %v\n%s", err, out)
	}
}
