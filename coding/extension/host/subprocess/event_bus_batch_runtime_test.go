package subprocess

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// The Node runtime serves a batched events.dispatch (handlerIds) as the unbatched requests it replaces: every listener the registry holds runs its synchronous prefix in id order over the one decoded payload, and an id the registry no longer holds (or holds for another channel) fails the request only after the others ran, as one failed request per listener did. A lone handlerId keeps its own form.
func TestNodeSharedBusBatchedDispatchRunsEveryKnownListener(t *testing.T) {
	nodeCellRequireNode(t)
	path, err := filepath.Abs("runtime-node/event-bus.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import {pathToFileURL} from "node:url";
const {SharedBus} = await import(pathToFileURL(%q));
const realm = {decode: (value) => value?.$x === "json" ? value.v : value};
const calls = [];
const runtime = {busCall: (method, args) => { calls.push([method, args]); }};
const bus = new SharedBus(realm, true);
const seen = [];
bus.on(runtime, "c", (data) => { seen.push(["a", data.n]); data.n++; });
bus.on(runtime, "c", (data) => { seen.push(["b", data.n]); data.n++; });
bus.on(runtime, "other", (data) => { seen.push(["other", data.n]); });
bus.on(runtime, "c", (data) => { seen.push(["d", data.n]); });
const ids = calls.filter(([method]) => method === "events.on").map(([, args]) => args.handlerId);
assert.equal(ids.length, 4);
const data = {$x: "json", v: {n: 0}};
const continuations = bus.dispatch({handlerIds: [ids[0], ids[1], ids[3]], channel: "c", data});
assert.equal(continuations.length, 3);
assert.deepEqual(seen, [["a", 0], ["b", 1], ["d", 2]]);
seen.length = 0;
assert.throws(() => bus.dispatch({handlerIds: [ids[0], "missing", ids[2], ids[3]], channel: "c", data: {$x: "json", v: {n: 10}}}), /Unknown event bus handler: missing/);
assert.deepEqual(seen, [["a", 10], ["d", 11]], "a batch skipped the listeners around an unknown id");
seen.length = 0;
bus.dispatch({handlerId: ids[2], channel: "other", data: {$x: "json", v: {n: 7}}});
assert.deepEqual(seen, [["other", 7]]);
assert.throws(() => bus.dispatch({handlerId: "missing", channel: "c", data}), /Unknown event bus handler: missing/);
`, path)
	cmd := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("node: %v\n%s", err, out)
	}
}
