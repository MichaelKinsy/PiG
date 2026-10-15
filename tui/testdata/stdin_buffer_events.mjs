// Compare the installed Pi StdinBuffer (an EventEmitter subclass), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { StdinBuffer } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const EVENTS = ["data", "paste"];
const results = JSON.parse(input).map((program) => {
  const buffer = new StdinBuffer({ timeout: 10000, escapeTimeout: 10000 });
  const listeners = new Map();
  let calls = [];
  const listener = (id) => {
    if (!listeners.has(id)) {
      const fn = (data) => {
        calls.push(`${id}:${JSON.stringify(data)}`);
        const action = program.actions?.[id];
        if (action?.add) buffer.on(action.add.event, listener(action.add.id));
        if (action?.remove) buffer.off(action.remove.event, listener(action.remove.id));
      };
      fn.id = id;
      listeners.set(id, fn);
    }
    return listeners.get(id);
  };
  const idOf = (fn) => fn.id ?? "?";
  const states = program.ops.map((op) => {
    calls = [];
    let ret = null;
    let error = "";
    try {
      switch (op.op) {
        case "on": buffer.on(op.event, listener(op.id)); break;
        case "addListener": buffer.addListener(op.event, listener(op.id)); break;
        case "once": buffer.once(op.event, listener(op.id)); break;
        case "prepend": buffer.prependListener(op.event, listener(op.id)); break;
        case "prependOnce": buffer.prependOnceListener(op.event, listener(op.id)); break;
        case "off": buffer.off(op.event, listener(op.id)); break;
        case "removeListener": buffer.removeListener(op.event, listener(op.id)); break;
        case "removeAll": if (op.event) buffer.removeAllListeners(op.event); else buffer.removeAllListeners(); break;
        case "emit": ret = buffer.emit(op.event, op.data); break;
        case "process": buffer.process(op.data); break;
        case "flushTimeout": for (const s of buffer.flush()) buffer.emit("data", s); break;
        default: throw new Error("op " + op.op);
      }
    } catch (e) {
      error = e.code ?? e.name;
    }
    const state = {
      ret,
      error,
      calls,
      names: buffer.eventNames(),
      count: {},
      listeners: {},
      raw: {},
    };
    for (const event of EVENTS) {
      state.count[event] = buffer.listenerCount(event);
      state.listeners[event] = buffer.listeners(event).map(idOf);
      state.raw[event] = buffer.rawListeners(event).map((fn) => (fn.listener ? `once(${idOf(fn.listener)})` : idOf(fn)));
    }
    if (op.countOf) state.countOf = buffer.listenerCount(op.countOf.event, listener(op.countOf.id));
    return state;
  });
  buffer.destroy();
  return { states };
});
process.stdout.write(JSON.stringify(results));
