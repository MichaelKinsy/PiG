import { pathToFileURL } from "node:url";
const root = pathToFileURL(`${process.argv[2]}/`).href;
const { createPeer } = await import(`${root}packages/coding-agent/src/experimental/mini/shared/rpc.ts`);
const { parentConnection } = await import(`${root}packages/coding-agent/src/experimental/mini/shared/transport.ts`);
const peer = createPeer(parentConnection(), {
  deadMs: 0,
  forward: async (method, args) => ({ method, args }),
});
const token = { name: "test" };
peer.provide(token, {
  echo(value) { return value; },
  void() {},
  fail() { throw new Error("broken"); },
  nested(value) { return peer.call("client.echo", value); },
  emit(to) {
    peer.emit(token, { type: "broadcast" });
    peer.emitTo(token, { type: "addressed" }, to);
  },
  wait(label, signal) {
    peer.emit(token, { type: "started", label });
    return new Promise((resolve) => signal.addEventListener("abort", () => {
      peer.emit(token, { type: "cancelled", reason: signal.reason.message });
      resolve("finished");
    }, { once: true }));
  },
});
