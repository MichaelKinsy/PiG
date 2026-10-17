// Drive the installed Pi's createEventBus with seeded subscribe, unsubscribe, emit and clear sequences: which handlers run in which order with which data, what an emit throws, which handler failures are reported, and handlers that change the subscriptions during an emit.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createEventBus } = await import(pathToFileURL(root + "dist/core/event-bus.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
const logged = [];
console.error = (message) => logged.push(String(message).replace(/\(([^)]*)\).*/s, "($1)"));
for (const scenario of JSON.parse(input)) {
  const bus = createEventBus();
  const unsubscribe = new Map();
  const calls = [];
  const outs = [];
  for (const op of scenario.ops) {
    calls.length = 0;
    logged.length = 0;
    let thrown = null;
    try {
      switch (op.op) {
        case "on": {
          const handler = (data) => {
            calls.push(`${op.id}:${JSON.stringify(data)}`);
            if (op.behavior === "throw") throw new Error("boom");
            if (op.behavior === "reject") return Promise.reject(new Error("later"));
            if (op.behavior === "unsubscribeSelf") unsubscribe.get(op.id)?.();
            if (op.behavior === "subscribeNew") unsubscribe.set(op.id + "n", bus.on(op.channel, () => calls.push(`${op.id}n:${JSON.stringify(data)}`)));
            if (op.behavior === "clear") bus.clear();
            if (op.behavior === "emitNested") bus.emit(op.nested, "nested");
          };
          unsubscribe.set(op.id, bus.on(op.channel, handler));
          break;
        }
        case "off": unsubscribe.get(op.id)?.(); break;
        case "emit": bus.emit(op.channel, op.data); break;
        case "clear": bus.clear(); break;
      }
    } catch (error) { thrown = String(error && error.message).slice(0, 60); }
    await new Promise((resolve) => setImmediate(resolve));
    outs.push({ calls: [...calls], thrown, logged: [...logged] });
  }
  results.push(outs);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
