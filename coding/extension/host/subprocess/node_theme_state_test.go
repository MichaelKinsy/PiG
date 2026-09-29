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
