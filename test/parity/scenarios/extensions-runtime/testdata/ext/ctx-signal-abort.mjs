// ctx.signal is the signal of the run in progress (runner.ts:917-920, agent-session.ts:3368): defined during the run, aborted with it, undefined once the run ends.
// The turn_start handler stays in flight until the user's abort reaches its ctx.signal; the lifecycle handlers report it at the end of the run and at settlement. Each state goes to its own footer status, so none replaces another.
export default function (pi) {
  pi.on("turn_start", async (_event, ctx) => {
    const signal = ctx.signal;
    ctx.ui.setStatus("sig1", `signal-live:${signal !== undefined && !signal.aborted}`);
    await new Promise((resolve) => signal.addEventListener("abort", resolve, { once: true }));
    ctx.ui.setStatus("sig2", `signal-aborted:${signal.aborted}`);
  });
  pi.on("agent_end", (_event, ctx) => ctx.ui.setStatus("sig3", `signal-end:${ctx.signal?.aborted}`));
  pi.on("agent_settled", (_event, ctx) => {
    ctx.ui.setStatus("sig4", `signal-settled:${ctx.signal === undefined}`);
    // Ends the compared crop after the last status.
    ctx.ui.setStatus("sig5", "signal-done");
  });
}
