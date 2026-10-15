import { access, readdir, stat } from "node:fs/promises";

// One command, /probe, whose handler is chosen by RPC_WINDOW_KIND. Each kind
// settles through a different event-loop route after the line that starts it.
const handlers = {
  microimmediate: async () => { await null; await new Promise((resolve) => setImmediate(resolve)); },
  awaits5immediate: async () => { for (let i = 0; i < 5; i++) await null; await new Promise((resolve) => setImmediate(resolve)); },
  immediate2: () => new Promise((resolve) => setImmediate(() => setImmediate(resolve))),
  fsstat: () => stat(process.cwd()),
  fsaccess: () => access(process.cwd()),
  fsreaddir: () => readdir(process.cwd()),
};

export default function (pi) {
  pi.registerCommand("probe", { description: "probe", handler: handlers[process.env.RPC_WINDOW_KIND] });
}
