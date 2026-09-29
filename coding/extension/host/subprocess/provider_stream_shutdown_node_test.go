package subprocess

import (
	"os/exec"
	"testing"
)

func TestNodeProviderTransportShutdownDrainsUnsettledStream(t *testing.T) {
	script := `
import assert from "node:assert/strict";
import {Runtime,ModelEventStream} from "./runtime-node/runtime.mjs";
// Pi 0.87.1 packages/ai/src/utils/event-stream.ts:43-89: request abort is not stream completion. Shutdown must drain only the transport-owned waiter.
for (const method of ["stream", "streamSimple", "fetchDeferred"]) for (const disconnect of [false,true]) {
 const runtime = new Runtime("unsettled.mjs");
 const stream = new ModelEventStream();
 let admitted;
 const ready = new Promise(resolve => { admitted = resolve; });
 const iterator = stream[Symbol.asyncIterator]();
 stream[Symbol.asyncIterator] = () => ({next() { const pending = iterator.next(); admitted(); return pending; }});
 runtime.nativeProviderCallbacks.set("owner", {[method]: () => stream});
 runtime.notify = () => {};
 const responses = [];
 let requestSent = false;
 runtime.connect = async () => {runtime.conn = {
   next: async () => {
     if (!requestSent) { requestSent = true; return {type:"request",id:"request",request:{method:"provider_stream",tool:"owner",args:{method,params:{model:{},context:{},handle:{}}}}}; }
     await ready;
     return disconnect ? null : {type:"shutdown"};
   },
   requestState: () => {}, cancelParent: () => {},
   respond: (id,result,error) => responses.push({id,result,error}),
 }};
 try { await runtime.run(); }
 finally {
   // Always drain the original pending iterator, including on the unfixed runtime.
   stream.push({type:"done",message:{late:true}});
   await Promise.allSettled([...runtime.requestTasks]);
 }
 assert.equal(responses.length, 1);
 assert.match(responses[0].error?.message ?? "", /closed/);
 assert.deepEqual(await stream.result(), {late:true});
 assert.equal(runtime.requestTasks.size, 0);
 assert.equal(runtime.activeRequests.size, 0);
}
`
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("Provider transport shutdown: %v\n%s", err, output)
	}
}

func TestNodeProviderTransportPreservesIteratorCleanup(t *testing.T) {
	script := `
import assert from "node:assert/strict";
import {getEventListeners} from "node:events";
import {Runtime} from "./runtime-node/runtime.mjs";
import {dispatchProviderObject} from "./runtime-node/provider-object.mjs";
// Pi's for-await loop closes the iterator when event delivery throws. Transport cancellation must not remove ordinary iterator cleanup.
for (const fail of [false,true]) {
 const runtime = new Runtime("cleanup.mjs");
 let closed = false;
 const stream = {
  async *[Symbol.asyncIterator]() {
   try { for (let index=0; index<1024; index++) yield {type:"text_delta",index}; }
   finally { closed = true; }
  },
  result() { throw new Error("stable dispatch must not add a result wait"); },
 };
 runtime.nativeProviderCallbacks.set("owner",{stream:()=>stream});
 const events = [];
 runtime.notify = (_method,args) => {
  if (args.result.type === "provider_started") return;
  if (fail) throw new Error("write failed");
  events.push(args.result.index);
 };
 const signal = new AbortController();
 const pending = dispatchProviderObject(runtime,"request",{tool:"owner",args:{method:"stream",params:{model:{},context:{}}}},{signal:signal.signal});
 if (fail) await assert.rejects(pending,/write failed/);
 else { assert.equal(await pending,null); assert.deepEqual(events,Array.from({length:1024},(_,index)=>index)); }
 assert.equal(closed,true,"event iterator was not closed");
 assert.equal(getEventListeners(signal.signal,"abort").length,0);
 assert.equal(getEventListeners(runtime.providerTransport.signal,"abort").length,0);
}
`
	if output, err := exec.CommandContext(t.Context(), "node", "--input-type=module", "--eval", script).CombinedOutput(); err != nil {
		t.Fatalf("Provider iterator cleanup: %v\n%s", err, output)
	}
}
