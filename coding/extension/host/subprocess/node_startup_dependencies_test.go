package subprocess

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi's virtual-modules.ts:14-38 supplies the complete namespaces. Deferring unused imports must retain their exported values, shared keybindings, and synchronous theme helpers (theme.ts:1078-1097,1186-1205).
func TestNodeStartupDefersPresentationDependencies(t *testing.T) {
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	dir := t.TempDir()
	entry := filepath.Join(dir, "light.ts")
	write(t, entry, `export default (pi: any) => pi.registerCommand("light", { description: "loaded" });`)
	heavy := filepath.Join(dir, "heavy.ts")
	write(t, heavy, `import * as current from "@earendil-works/pi-tui";
import * as legacy from "@mariozechner/pi-tui";
import assert from "node:assert/strict";
export default () => {
  assert.deepEqual(Object.keys(current), Object.keys(legacy));
  for (const key of Object.keys(current)) assert.equal(current[key], legacy[key]);
  return current;
};`)
	script := `import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import { pathToFileURL } from "node:url";
let allowPresentation = false;
const loaded = new Set();
registerHooks({load(url, context, next) {
  if (/\/(pi-tui\/(index|tui|utils)\.js|components\/(markdown|text)\.js|highlight\.js\/|utils\/syntax-highlight\.js|typebox\.mjs|sdk-bundle\/)/.test(url)) {
    assert.ok(allowPresentation, "unused presentation/schema module loaded: " + url);
    loaded.add(url);
  }
  return next(url, context);
}});
const root = pathToFileURL(process.argv[1] + "/");
const { Runtime } = await import(new URL("runtime.mjs", root));
const { importExtension } = await import(new URL("jiti-loader.mjs", root));
const runtime = new Runtime(process.argv[2]);
const factory = await importExtension(process.argv[2]);
const commands = [];
factory({registerCommand: (name, options) => commands.push([name, options.description])});
assert.deepEqual(commands, [["light", "loaded"]]);
assert.notEqual(await importExtension(process.argv[2]), factory, "factories must be reevaluated");
assert.equal(loaded.size, 0);
allowPresentation = true;
const tui = (await importExtension(process.argv[3]))();
const native = await import("@earendil-works/pi-tui");
assert.deepEqual(Object.keys(tui), Object.keys(native));
for (const key of Object.keys(native)) assert.equal(tui[key], native[key]);
assert.equal(tui.getKeybindings(), runtime.keybindings());
const { highlightCode, getMarkdownTheme } = await import(new URL("shims/pi-dist/pi-coding-agent/modes/interactive/theme/theme.js", root));
assert.deepEqual(highlightCode("const answer = 42;", "javascript"), getMarkdownTheme().highlightCode("const answer = 42;", "javascript"));
assert.ok([...loaded].some(url => url.endsWith("/pi-tui/sdk-bundle/index.js")));
assert.ok([...loaded].some(url => url.endsWith("/utils/syntax-highlight.js")));
`
	cmd := exec.CommandContext(t.Context(), "node", "--import", registerLoaderURL(t, root), "--input-type=module", "--eval", script, root, entry, heavy)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("startup dependency boundary: %v\n%s", err, output)
	}
}
