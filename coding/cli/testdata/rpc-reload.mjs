// The extension of the RPC reload tests (rpc_reload_pipelined_test.go). Every wait is on a file in RL_DIR that the test creates, so a test decides each interleaving. The generation counts the factory's runs: a reload gives g2.
import { existsSync, readFileSync, writeFileSync } from "node:fs";

const dir = process.env.RL_DIR;
const exists = (name) => existsSync(dir + "/" + name);
const touch = (name) => writeFileSync(dir + "/" + name, "");
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const until = async (name) => { while (!exists(name)) await sleep(2); };
const blockUntil = (name) => { const cell = new Int32Array(new SharedArrayBuffer(4)); while (!exists(name)) Atomics.wait(cell, 0, 0, 2); };

export default async function (pi) {
  let gen = 1;
  try { gen = Number(readFileSync(dir + "/gen", "utf8")) + 1; } catch {}
  writeFileSync(dir + "/gen", String(gen));
  const g = "g" + gen;
  // The reloaded factory blocks its thread (no event-loop turn) or awaits timers (the loop turns) until the test lets it go.
  if (gen > 1 && process.env.RL_LOAD === "block") { touch("loading"); blockUntil("load-gate"); }
  if (gen > 1 && process.env.RL_LOAD === "await") { touch("loading"); await until("load-gate"); }
  pi.registerCommand("rl", { description: "reload", handler: async (_args, ctx) => { await ctx.reload(); } });
  pi.registerCommand("ping", { description: "ping", handler: async (_args, ctx) => { ctx.ui.notify("ping " + g, "info"); } });
  pi.registerCommand("held", { description: "held", handler: async (_args, ctx) => {
    ctx.ui.notify("held running " + g, "info");
    await until("held-gate");
    ctx.ui.notify("held done " + g, "info");
    // Written once the call was made: a stale ctx throws at ctx.ui, before it.
    touch("held-went-on");
  } });
  pi.registerCommand("ask", { description: "ask", handler: async (_args, ctx) => {
    // The answer is recorded without the host: the handler's ctx is stale once a reload has run.
    writeFileSync(dir + "/asked", String(await ctx.ui.input("gate")));
    ctx.ui.notify("ask done " + g, "info");
  } });
  pi.registerTool({
    name: "gate_tool", label: "gate", description: "waits for its gate", parameters: { type: "object", properties: {} },
    execute: async () => { touch("tool-running"); await until("tool-gate"); return { content: [{ type: "text", text: "tool done " + g }], details: {} }; },
  });
  if (process.env.RL_START === "send") pi.on("input", async (event, ctx) => { ctx.ui.notify("input " + g + ": " + event.text, "info"); });
  pi.on("session_start", async (event, ctx) => {
    if (event.reason !== "reload") return;
    ctx.ui.notify("started " + g, "info");
    if (process.env.RL_START === "await") { await until("start-gate"); ctx.ui.notify("start done " + g, "info"); }
    if (process.env.RL_START === "ask") { const value = await ctx.ui.input("start-ask"); ctx.ui.notify("start answered " + value + " " + g, "info"); }
    if (process.env.RL_START === "send") pi.sendUserMessage("hello from session_start");
  });
  pi.on("session_shutdown", async (event, ctx) => {
    if (event.reason !== "reload" || !process.env.RL_SHUTDOWN) return;
    ctx.ui.notify("shutting down " + g, "info");
    if (process.env.RL_SHUTDOWN === "await") await until("shutdown-gate");
    if (process.env.RL_SHUTDOWN === "ask") await ctx.ui.input("shutdown-ask");
    ctx.ui.notify("shutdown done " + g, "info");
  });
}
