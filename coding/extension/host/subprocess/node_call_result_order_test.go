package subprocess

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// An awaited host call cannot overtake state frames received before its result, even when the receive loop already has a backlog.
func TestNodeCallResultWaitsForEarlierFrames(t *testing.T) {
	nodeCellRequireNode(t)
	path, err := filepath.Abs("runtime-node/runtime.mjs")
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`
import assert from "node:assert/strict";
import {EventEmitter} from "node:events";
import {pathToFileURL} from "node:url";
const {Connection} = await import(pathToFileURL(%q));
const socket = new EventEmitter();
socket.write = () => {};
const conn = new Connection(socket);
let settled = false;
const pending = conn.call("ui.select").then(result => {settled = true; return result;});
const [id] = conn.pending.keys();
socket.emit("envelope", {type:"notify", notify:{method:"state_update", args:{toolsExpanded:false}}});
socket.emit("envelope", {type:"notify", notify:{method:"state_update", args:{toolsExpanded:true}}});
socket.emit("envelope", {type:"call_result", id, call_result:{result:{ok:true,selected:"chosen"}}});
await Promise.resolve();
assert.equal(settled,false,"call result overtook queued state");
assert.equal((await conn.next()).notify.args.toolsExpanded,false);
assert.equal((await conn.next()).notify.args.toolsExpanded,true);
conn.resolveCall(await conn.next());
assert.deepEqual(await pending,{ok:true,selected:"chosen"});
assert.equal(conn.pending.size,0);
const closingSocket = new EventEmitter();
closingSocket.write = () => {};
const closing = new Connection(closingSocket);
const delivered = closing.call("ui.select");
const [closingID] = closing.pending.keys();
closingSocket.emit("envelope", {type:"call_result", id:closingID, call_result:{result:{ok:true}}});
closingSocket.emit("close");
closing.resolveCall(await closing.next());
assert.deepEqual(await delivered,{ok:true},"close discarded an already received result");
assert.equal(await closing.next(),null);
assert.equal(closing.pending.size,0);
const cancelledSocket = new EventEmitter();
cancelledSocket.write = () => {};
const cancelled = new Connection(cancelledSocket);
let rejected = false;
const cancellation = cancelled.call("ui.select", {}, "parent").catch(error => { rejected = true; return error.message; });
cancelledSocket.emit("envelope", {type:"notify", notify:{method:"state_update", args:{toolsExpanded:true}}});
cancelledSocket.emit("envelope", {type:"cancel", cancel:{request_id:"parent"}});
await Promise.resolve();
assert.equal(rejected,false,"cancellation overtook queued state");
assert.equal((await cancelled.next()).notify.args.toolsExpanded,true);
assert.equal((await cancelled.next()).type,"cancel");
cancelled.cancelParent("parent");
assert.match(await cancellation,/cancelled with parent request/);
assert.equal(cancelled.pending.size,0);
`, path)
	if out, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "-e", script).CombinedOutput(); err != nil {
		t.Fatalf("receive ordering: %v\n%s", err, out)
	}
}
