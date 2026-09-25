import { pathToFileURL } from "node:url";
const root = pathToFileURL(`${process.argv[2]}/`).href;
const { createPeer } = await import(`${root}packages/coding-agent/src/experimental/mini/shared/rpc.ts`);
let receive;
let close;
const peer = createPeer({
  send() {},
  onMessage(handler) { receive = handler; },
  onClose(handler) { close = handler; },
  close() { close(); },
}, { deadMs: 0 });
const trace = [];
const token = { name: "lane" };
const implementation = (label) => ({
  async prompt(value) { trace.push(`${label}:${value}`); },
});
peer.provide(token, implementation("old"));
peer.onEvent((_service, payload) => {
  if (payload === "replace") {
    trace.push("event");
    peer.provide(token, implementation("new"));
  } else {
    trace.push("end");
  }
});
receive({ kind: "call", id: 1, method: "lane.prompt", args: [1] });
receive({ kind: "call", id: 2, method: "lane.prompt", args: [2] });
receive({ kind: "event", service: "lane", payload: "replace" });
receive({ kind: "call", id: 3, method: "lane.prompt", args: [3] });
receive({ kind: "event", service: "lane", payload: "end" });
peer.close();
console.log(JSON.stringify(trace));
