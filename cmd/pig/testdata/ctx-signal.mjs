import { appendFileSync } from "node:fs";

// Records what ctx.signal is at each extension entry point, as JSON lines in
// CTX_SIGNAL_REPORT. A signal is named by the order it first appears in, so a
// line shows whether two entry points saw the same object.
// Pi: ctx.signal is runner.getSignalFn(), AgentSession's `() => this.agent.signal`
// (agent-session.ts:3368, runner.ts:917-920): the current run's signal, undefined
// while no run is active.
const seen = new Map();
const name = (signal) => {
  if (signal === undefined) return "undefined";
  if (!seen.has(signal)) seen.set(signal, `s${seen.size + 1}`);
  return seen.get(signal);
};
const record = (where, ctx, extra = {}) => {
  const signal = ctx.signal;
  appendFileSync(process.env.CTX_SIGNAL_REPORT, JSON.stringify({ where, signal: name(signal), aborted: signal?.aborted, ...extra }) + "\n");
};
let ownSignal;
const hold = (signal, ms) => new Promise((resolve) => {
  if (!signal) return resolve("no-signal");
  const timer = setTimeout(() => resolve("timeout"), ms);
  const done = () => { clearTimeout(timer); resolve("aborted"); };
  if (signal.aborted) return done();
  signal.addEventListener("abort", done, { once: true });
});

export default function (pi) {
  for (const event of ["session_start", "agent_start", "tool_call", "tool_execution_start", "agent_end", "agent_settled"]) {
    pi.on(event, (_event, ctx) => record(event, ctx));
  }
  // CTX_SIGNAL_HOLD_TURN_START keeps the turn_start handler in flight until its signal aborts.
  if (process.env.CTX_SIGNAL_HOLD_TURN_START) {
    pi.on("turn_start", async (_event, ctx) => {
      record("turn_start", ctx);
      const outcome = await hold(ctx.signal, 3000);
      record("turn_start.held", ctx, { outcome });
    });
  }
  pi.registerCommand("probe", {
    description: "Record ctx.signal in a command",
    handler: async (_args, ctx) => record("command", ctx),
  });
  // inner is called through ctx.executeTool by echo_bridge when CTX_SIGNAL_NESTED is set. A call without a signal option runs with the calling tool's signal; a call with one runs with that signal (runner.ts:979-981, `options.signal ?? signal`).
  pi.registerTool({
    name: "inner",
    label: "Nested tool",
    description: "Report the signals of a nested call.",
    parameters: { type: "object", properties: {} },
    async execute(_id, _args, signal, _onUpdate, ctx) {
      const own = signal === ownSignal;
      record(own ? "inner.explicit" : "inner.default", ctx, { sameAsCtx: signal === ctx.signal, paramAborted: signal?.aborted });
      if (own) {
        const outcome = await hold(signal, 3000);
        record("inner.explicit.held", ctx, { outcome, paramAborted: signal.aborted });
      }
      return { content: [{ type: "text", text: "inner" }] };
    },
  });
  pi.registerTool({
    name: "echo_bridge",
    label: "Blocking tool",
    description: "Wait until the run is aborted.",
    parameters: { type: "object", properties: { text: { type: "string" } }, required: ["text"] },
    async execute(_id, _args, signal, onUpdate, ctx) {
      record("tool_execute", ctx, { sameAsCtx: signal === ctx.signal, paramAborted: signal?.aborted });
      onUpdate?.({ content: [{ type: "text", text: "running" }], details: {} });
      if (process.env.CTX_SIGNAL_NESTED) {
        await ctx.executeTool("inner", {});
        const controller = new AbortController();
        ownSignal = controller.signal;
        const nested = ctx.executeTool("inner", {}, { signal: controller.signal });
        await new Promise((resolve) => setTimeout(resolve, 300));
        controller.abort();
        await nested;
      }
      const outcome = await hold(signal, 3000);
      record("tool_execute.held", ctx, { outcome, paramAborted: signal?.aborted });
      return { content: [{ type: "text", text: "aborted" }] };
    },
  });
}
