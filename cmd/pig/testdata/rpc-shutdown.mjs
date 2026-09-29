import { appendFileSync, readFile as readFileCallback } from "node:fs";
import { readFile } from "node:fs/promises";

// Records every lifecycle event the extension receives. echo_bridge reports a
// tool update and stays in flight until its run is aborted. session_shutdown
// reports that it started through a notification. Then RPC_SHUTDOWN_BLOCK
// makes it wait on a promise that never settles, RPC_SHUTDOWN_SLOW makes it wait that many milliseconds, RPC_SHUTDOWN_HOLD also keeps
// the event loop alive, and RPC_SHUTDOWN_DIALOG makes it wait on a select.
// The ask command waits on a select; the quit command calls ctx.shutdown().
export default function (pi) {
  if (process.env.RPC_SHUTDOWN_BAD_RESOURCE) {
    pi.on("resources_discover", () => ({ skillPaths: ["file:///a%2Fb"] }));
  }
  const record = (name) => appendFileSync(process.env.RPC_SHUTDOWN_REPORT, name + "\n");
  const events = ["agent_start", "turn_start", "message_start", "message_end", "tool_execution_start", "tool_execution_end", "turn_end", "agent_end", "agent_settled"];
  for (const event of events) {
    pi.on(event, () => record(event));
  }
  // RPC_SHUTDOWN_TURN_END_STATUS makes turn_end call the host while the wait command awaits waitForIdle.
  if (process.env.RPC_SHUTDOWN_TURN_END_STATUS) {
    pi.on("turn_end", (_event, ctx) => ctx.ui.setStatus("probe", "turn"));
  }
  pi.registerCommand("wait", {
    description: "Wait for the run to end",
    handler: async (_args, ctx) => {
      await ctx.waitForIdle();
      record("wait:idle");
      ctx.ui.notify("idle reached", "info");
    },
  });
  if (process.env.RPC_SHUTDOWN_TAIL_DELAY) {
    pi.on("turn_end", async () => {
      await new Promise(resolve => setTimeout(resolve, 50));
      record("tail_timer");
    });
  }
  if (process.env.RPC_SHUTDOWN_ON_SETTLED) {
    pi.on("agent_settled", (_event, ctx) => ctx.shutdown());
  }
  // RPC_SHUTDOWN_NO_HANDLER leaves shutdown() without a session_shutdown handler, so its dispose does not wait on a host call.
  if (!process.env.RPC_SHUTDOWN_NO_HANDLER) pi.on("session_shutdown", async (event, ctx) => {
    record("session_shutdown");
    // Before the notification: in Pi the quit shutdown reaches the replaced Session's stale extension instance, where ctx.ui.notify throws and would end the handler instead of blocking it.
    if (process.env.RPC_SHUTDOWN_BLOCK_QUIT && event.reason === "quit") {
      await new Promise(() => {});
    }
    // RPC_SHUTDOWN_HOLD_QUIT keeps the event loop alive while a quit shutdown never settles, so the process cannot exit before a command that started earlier answers.
    if (process.env.RPC_SHUTDOWN_HOLD_QUIT && event.reason === "quit") {
      await new Promise(() => { setInterval(() => {}, 60_000); });
    }
    ctx.ui.notify("session_shutdown started", "info");
    if (process.env.RPC_SHUTDOWN_SLOW) {
      await new Promise(resolve => setTimeout(resolve, Number(process.env.RPC_SHUTDOWN_SLOW)));
    }
    if (process.env.RPC_SHUTDOWN_BLOCK) {
      await new Promise(() => {});
    }
    if (process.env.RPC_SHUTDOWN_HOLD) {
      await new Promise(() => {
        setInterval(() => {}, 60_000);
      });
    }
    if (process.env.RPC_SHUTDOWN_DIALOG) {
      record("dialog:" + (await ctx.ui.select("Shutdown dialog", ["keep"])));
    }
  });
  pi.registerCommand("ask", {
    description: "Wait on a select",
    handler: async (_args, ctx) => {
      record("ask:" + (await ctx.ui.select("Ask", ["yes"])));
    },
  });
  // Each command settles through a different Node event-loop phase, to compare
  // which of them Pi answers when stdin ends right after the line that starts
  // it. Pi reads stdin's end one event-loop iteration after that line, then
  // shutdown() exits within microtasks and one tick
  // (rpc-mode.ts:728-744, output-guard.ts:105-108).
  const settle = (name, wait) => pi.registerCommand(name, {
    description: "Settle through " + name,
    handler: async () => {
      await wait();
      record(name);
    },
  });
  settle("sync", async () => {});
  settle("micro", async () => {
    await Promise.resolve();
    await null;
  });
  settle("nexttick", () => new Promise(resolve => process.nextTick(resolve)));
  settle("immediate", () => new Promise(resolve => setImmediate(resolve)));
  // The immediate is queued from a microtask continuation, after the handler's synchronous prefix. Pi answers both shapes (10 of 10 runs).
  settle("microimmediate", async () => {
    await null;
    await new Promise(resolve => setImmediate(resolve));
  });
  settle("awaits5immediate", async () => {
    for (let i = 0; i < 5; i++) await null;
    await new Promise(resolve => setImmediate(resolve));
  });
  settle("immediate2", () => new Promise(resolve => setImmediate(() => setImmediate(resolve))));
  // A delay no test run reaches: the handler never resumes in Pi, which exits first.
  settle("timer", () => new Promise(resolve => setTimeout(resolve, 60_000)));
  settle("timer50", () => new Promise(resolve => setTimeout(resolve, 50)));
  settle("fspromises", () => readFile(new URL(import.meta.url)));
  settle("fscallback", () => new Promise((resolve, reject) => readFileCallback(new URL(import.meta.url), (err, data) => (err ? reject(err) : resolve(data)))));
  // pi.exec is child-process I/O that shutdown() does not await.
  settle("exec", () => pi.exec("sleep", ["5"]));
  pi.registerCommand("execloop", {
    description: "Run child processes without end",
    handler: async () => {
      for (;;) await pi.exec("true", []);
    },
  });
  // ns replaces the Session and records that the command resumed; nstimer then waits on a timer no test run reaches. RPC_SHUTDOWN_BLOCK_QUIT makes a quit session_shutdown wait forever, so a replacement still shuts the old Session down.
  pi.registerCommand("ns", {
    description: "Replace the Session",
    handler: async (_args, ctx) => {
      await ctx.newSession();
      record("ns-done");
    },
  });
  pi.registerCommand("nstimer", {
    description: "Replace the Session, then wait on a timer",
    handler: async (_args, ctx) => {
      await ctx.newSession();
      record("ns-done");
      await new Promise(resolve => setTimeout(resolve, 60_000));
      record("timer-done");
    },
  });
  pi.registerCommand("quit", {
    description: "Request shutdown",
    handler: async (_args, ctx) => {
      record("quit");
      if (_args === "title") ctx.ui.setTitle("quitting");
      ctx.shutdown();
    },
  });
  pi.registerTool({
    name: "echo_bridge",
    label: "Blocking tool",
    description: "Wait until the run is aborted.",
    parameters: { type: "object", properties: { text: { type: "string" } }, required: ["text"] },
    async execute(_id, _args, signal, onUpdate) {
      record("tool_running");
      onUpdate?.({ content: [{ type: "text", text: "running" }], details: {} });
      await new Promise((resolve) => {
        if (signal?.aborted) return resolve();
        signal?.addEventListener("abort", () => resolve(), { once: true });
      });
      return { content: [{ type: "text", text: "aborted" }] };
    },
  });
}
