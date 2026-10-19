package subprocess

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestNodeEventStreamReplicas: the Node runtime keeps a keyframe, applies each delta to it as the frame arrives, hands every handler its own copy, and refuses a delta numbered against a payload it does not hold.
func TestNodeEventStreamReplicas(t *testing.T) {
	path := filepath.Join(findModuleRoot(t), "coding", "extension", "host", "subprocess", "runtime-node", "event-stream.mjs")
	script := `import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
const {EventReplicas} = await import(pathToFileURL(process.argv[1]));
const request = (stream, args) => ({event: "message_update", stream, ...(args === undefined ? {} : {args})});
const replicas = new EventReplicas();
const first = request({seq: 1}, {type: "message_update", text: "ab", list: [1], nested: {k: "v"}});
replicas.apply(first);
assert.equal(first.streamError, undefined);
// A handler edits its copy; the next delta must not see it.
first.args.text = "edited"; first.args.nested.k = "edited"; first.args.list[0] = "edited";
const second = request({seq: 2, base: 1, ops: [
  {op: "append", path: ["text"], value: "cd"}, {op: "set", path: ["list", 1], value: 2},
  {op: "set", path: ["nested", "new"], value: null}, {op: "set", path: ["added"], value: {x: [true]}},
  {op: "set", path: ["__proto__"], value: {polluted: true}}]});
replicas.apply(second);
assert.equal(second.streamError, undefined);
assert.deepEqual(Object.keys(second.args), ["type", "text", "list", "nested", "added", "__proto__"]);
assert.equal(second.args.text, "abcd");
assert.deepEqual(second.args.list, [1, 2]);
assert.deepEqual(second.args.nested, {k: "v", new: null});
assert.equal(({}).polluted, undefined);
assert.equal(Object.getPrototypeOf(second.args), Object.prototype);
// The payloads of another event name are separate.
const other = {event: "tool_execution_update", stream: {seq: 9}, args: {b: "y"}};
replicas.apply(other);
const third = request({seq: 3, base: 2, ops: [{op: "append", path: ["text"], value: "!"}]});
replicas.apply(third);
assert.equal(third.args.text, "abcd!");
// Each key of an event name has its own payload and numbering, and a frame's evict list drops keys once it applied.
const keyed = (key, stream, args) => ({event: "tool_execution_update", stream: {key, ...stream}, ...(args === undefined ? {} : {args})});
const plus = [{op: "append", path: ["text"], value: "+"}];
const calls = new EventReplicas();
calls.apply(keyed("a", {seq: 1}, {text: "a"}));
calls.apply(keyed("b", {seq: 1}, {text: "b"}));
const b1 = keyed("b", {seq: 2, base: 1, ops: plus});
calls.apply(b1);
assert.equal(b1.args.text, "b+");
const a1 = keyed("a", {seq: 2, base: 1, ops: plus});
calls.apply(a1);
assert.equal(a1.args.text, "a+");
calls.apply(keyed("c", {seq: 1, evict: ["a"]}, {text: "c"}));
const stale = keyed("a", {seq: 3, base: 2, ops: plus});
calls.apply(stale);
assert.ok(stale.streamError instanceof Error, "a delta applied to an evicted call");
const b2 = keyed("b", {seq: 3, base: 2, ops: plus});
calls.apply(b2);
assert.equal(b2.args.text, "b++");
// A delta for a payload the runtime does not hold is an error for its request, and drops the replica.
for (const [name, ops, base] of [["wrong base", [], 7], ["append to a number", [{op: "append", path: ["list", 0], value: "x"}], 3], ["bad path", [{op: "set", path: ["no", "where"], value: 1}], 3], ["array past the end", [{op: "set", path: ["list", 9], value: 1}], 3], ["unknown op", [{op: "del", path: ["text"]}], 3]]) {
  const fresh = new EventReplicas();
  fresh.apply(request({seq: 3}, {type: "t", text: "x", list: [1]}));
  const bad = request({seq: 4, base, ops});
  fresh.apply(bad);
  assert.ok(bad.streamError instanceof Error, name);
  const after = request({seq: 5, base: 3, ops: []});
  fresh.apply(after);
  assert.ok(after.streamError instanceof Error, name + ": the replica was kept after a failure");
}
`
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("event stream replicas: %v\n%s", err, output)
	}
}
