//go:build !pig_strip_node_extensions

package subprocess

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The virtual modules in Pi's core/extensions/virtual-modules.ts:14-38 share exported values. Deferring their loading must not replace those values or cache extension factories (core/extensions/loader.ts:491-510).
func TestNodeColdStartLoadsSDKOnlyOnImport(t *testing.T) {
	t.Parallel()
	root := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node")
	dir := t.TempDir()
	entry := filepath.Join(dir, "light.ts")
	if err := os.WriteFile(entry, []byte(`export default (pi: any) => { pi.events.emit("cold", "ready"); };`), 0o600); err != nil {
		t.Fatal(err)
	}
	script := `
import assert from "node:assert/strict";
import { registerHooks } from "node:module";
import { pathToFileURL } from "node:url";
let allowSDK = false;
let sdkLoaded = false;
registerHooks({ load(url, context, next) {
 if (url.includes("/sdk-bundle/")) {
   assert.ok(allowSDK, "unused SDK closure loaded before an extension imports it");
   sdkLoaded = true;
 }
 return next(url, context);
}});
const root = pathToFileURL(process.argv[1] + "/");
const { Runtime } = await import(new URL("runtime.mjs", root));
const { importExtension } = await import(new URL("jiti-loader.mjs", root));
const runtime = new Runtime("cold");
const calls = [];
const factory = await importExtension(process.argv[2]);
factory({ events: { emit: (...args) => calls.push(args) } });
assert.deepEqual(calls, [["cold", "ready"]]);
assert.notEqual(await importExtension(process.argv[2]), factory);
const { disposeIndependentSessions } = await import(new URL("independent-session-owner.mjs", root));
await disposeIndependentSessions(runtime);
allowSDK = true;
const imported = await importExtension(process.argv[3]);
const sdk = await import("@earendil-works/pi-coding-agent");
// Jiti wraps each namespace independently, as in Pi. The exported values stay shared.
assert.deepEqual(Object.keys(imported()), Object.keys(sdk));
for (const key of Object.keys(sdk)) assert.equal(imported()[key], sdk[key]);
assert.ok(sdkLoaded);
assert.equal(typeof sdk.createAgentSession, "function");
const legacy = await import("@mariozechner/pi-coding-agent");
assert.equal(sdk, legacy);
`
	heavy := filepath.Join(dir, "heavy.ts")
	if err := os.WriteFile(heavy, []byte(`import * as current from "@earendil-works/pi-coding-agent";
import * as legacy from "@mariozechner/pi-coding-agent";
import assert from "node:assert/strict";
export default () => {
  assert.deepEqual(Object.keys(current), Object.keys(legacy));
  for (const key of Object.keys(current)) assert.equal(current[key], legacy[key]);
  return current;
};`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "--import", registerLoaderURL(t, root), "--input-type=module", "--eval", script, root, entry, heavy)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cold module boundary: %v\n%s", err, output)
	}
}
