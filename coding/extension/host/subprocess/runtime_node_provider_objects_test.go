package subprocess

import (
	"os/exec"
	"testing"
)

// Pi model-runtime.ts:744-750,793-798 retains the original native Provider and removes it globally on unregister.
func TestNodeProviderObjectsFollowCellOwnership(t *testing.T) {
	script := `
import assert from "node:assert/strict";
import { Runtime } from "./runtime-node/runtime.mjs";
const objects = new Map();
const owner = new Runtime("owner.mjs", objects);
const reader = new Runtime("reader.mjs", objects);
const makeProvider = name => ({id:"native",name,auth:{apiKey:{name:"test",resolve:async()=>({auth:{apiKey:name}})}},getModels:()=>[]});
const first = makeProvider("first");
owner.registerNativeProvider(first);
assert.equal(reader.ctx.modelRegistry.getRegisteredNativeProvider("native"),undefined,"uncommitted factory leaked");
owner.commitLoad();
reader.commitLoad();
assert.strictEqual(reader.ctx.modelRegistry.getRegisteredNativeProvider("native"),first,"same-cell carrier missing");
assert.strictEqual(reader.ctx.modelRegistry.getProvider("native"),first);
assert.equal(new Runtime("isolated.mjs").ctx.modelRegistry.getRegisteredNativeProvider("native"),undefined,"separate cell leaked");
const failed = new Runtime("failed.mjs",objects);
failed.registerNativeProvider(makeProvider("failed"));
failed.discardLoad();
assert.strictEqual(reader.ctx.modelRegistry.getProvider("native"),first,"failed factory replaced a provider");
const second = makeProvider("second");
reader.registerNativeProvider(second);
assert.strictEqual(owner.ctx.modelRegistry.getProvider("native"),second,"replacement not shared");
owner.connect=async()=>{owner.conn={next:async()=>({type:"shutdown"})}};
await owner.run();
assert.strictEqual(reader.ctx.modelRegistry.getProvider("native"),second,"old owner removed replacement");
reader.unregisterProvider("native");
assert.equal(reader.ctx.modelRegistry.getRegisteredNativeProvider("native"),undefined);
reader.registerNativeProvider(second);
reader.registerProvider("native",{api:"openai-completions",baseUrl:"http://127.0.0.1:9",apiKey:"test",models:[]});
assert.equal(reader.ctx.modelRegistry.getRegisteredNativeProvider("native"),undefined,"config replacement kept native object");
reader.registerNativeProvider(second);
reader.connect=async()=>{reader.conn={next:async()=>({type:"shutdown"})}};
await reader.run();
assert.equal(objects.size,0,"provider retained after owner shutdown");
const disconnected = new Runtime("disconnected.mjs",objects);
disconnected.registerNativeProvider(first);
disconnected.commitLoad();
disconnected.connect=async()=>{throw new Error("connect failed")};
await assert.rejects(disconnected.run(),/connect failed/);
assert.equal(objects.size,0,"provider retained after connection failure");
`
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("Provider ownership: %v\n%s", err, output)
	}
}
