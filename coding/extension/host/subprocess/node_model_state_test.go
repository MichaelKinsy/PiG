package subprocess

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// Pi loader.ts:406-408 delegates the awaited model object to agent-session.ts:3087-3090; failed selection must not fabricate context state.
func TestNodeSetModelAwaitsAuthoritativeState(t *testing.T) {
	nodeCellRequireNode(t)
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
const {Runtime} = await import(pathToFileURL(%q));
const r = new Runtime("/ext/model.mjs");
r.hostReady = true;
const old = {provider:"p", id:"old", contextWindow:100};
const next = {provider:"p", id:"new", contextWindow:128000};
r.applyState({model:old});
let resolve, reject;
r.conn = {requestState(){}, call(method,args) {
 if (method === "ui.setHeader" || method === "ui.setFooter") return Promise.resolve({});
 assert.equal(method, "setModel");
 assert.deepEqual(args, {model:"p/new"});
 return new Promise((yes,no) => {resolve=yes; reject=no;});
}};
let settled = false;
const pending = r.api.setModel(next);
assert.equal(typeof pending?.then, "function", "setModel returns a Promise");
pending.then(() => {settled=true;});
await Promise.resolve();
assert.equal(settled, false);
assert.equal(r.ctx.model.id, "old", "no optimistic model");
r.applyState({model:next, contextUsage:{tokens:null, contextWindow:128000, percent:null}});
resolve({success:true});
assert.equal(await pending, true);
assert.equal(r.ctx.model.id, "new");
assert.equal(r.ctx.getContextUsage().tokens, null);
const failed = r.api.setModel(next);
resolve({success:false});
assert.equal(await failed, false);
assert.equal(r.ctx.model.id, "new");
const cancelled = r.api.setModel(next);
reject(new Error("cancelled"));
await assert.rejects(cancelled, /cancelled/);
`, path)
	if out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("setModel: %v\n%s", err, out)
	}
}
