import { pathToFileURL } from "node:url";
import { resolve } from "node:path";

const { InteractiveMode } = await import(pathToFileURL(resolve("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/modes/interactive/interactive-mode.js")));
class Exit extends Error {}
const originalExit = process.exit;
try {
  process.exit = () => { throw new Exit(); };
  for (const fromSignal of [true, false]) {
    const order = [];
    const state = {
      isShuttingDown: false,
      unregisterSignalHandlers() {},
      runtimeHost: { async dispose() { order.push("dispose"); } },
      ui: { terminal: { async drainInput() { order.push("drainInput"); } } },
      themeController: { disableAutoSync() {} },
      stop() { order.push("stop"); },
      sessionManager: { isPersisted: () => false, getSessionFile: () => undefined },
    };
    try { await InteractiveMode.prototype.shutdown.call(state, { fromSignal }); }
    catch (error) { if (!(error instanceof Exit)) throw error; }
    const expected = fromSignal ? "dispose,drainInput,stop" : "drainInput,stop,dispose";
    if (order.join(",") !== expected || !state.isShuttingDown) throw new Error(`shutdown: ${JSON.stringify(order)}`);
    console.log(`SHUTDOWN_ORDER ${fromSignal ? "signal" : "interactive"} ${order.join(",")}`);
  }
} finally { process.exit = originalExit; }
