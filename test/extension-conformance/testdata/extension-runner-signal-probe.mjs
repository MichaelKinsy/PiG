// The probe extension of the extension_runner_signal row (extension_runner_signal_test.go): it keeps the signal `ctx.signal` is at `agent_start` and counts the abort events of every signal it sees. At `turn_start` and `message_start` it records, through pi.appendEntry (which the script's bound appendEntry action logs), whether `ctx.signal` is still the kept one, which of the two has aborted and how many abort events each has had. Pi reads `ctx.signal` at access time (runner.ts:940-943) from `agent.signal`, the signal of the run that is active (agent.ts:345-347), so a handler that keeps run 1's signal never sees run 2's abort.
export default function (pi) {
  let held;
  const events = new Map();
  const watch = (signal) => {
    if (!signal || events.has(signal)) return;
    events.set(signal, 0);
    signal.addEventListener("abort", () => events.set(signal, events.get(signal) + 1));
  };
  const note = (kind, ctx) =>
    pi.appendEntry("signal_" + kind, {
      signalIsHeld: ctx.signal === held,
      heldAborted: held.aborted,
      heldAbortEvents: events.get(held),
      currentAborted: ctx.signal ? ctx.signal.aborted : null,
      currentAbortEvents: ctx.signal ? events.get(ctx.signal) : null,
    });
  pi.on("agent_start", async (_event, ctx) => {
    held = ctx.signal;
    watch(held);
  });
  pi.on("turn_start", async (_event, ctx) => {
    watch(ctx.signal);
    note("turn_start", ctx);
  });
  pi.on("message_start", async (_event, ctx) => {
    watch(ctx.signal);
    note("message_start", ctx);
  });
}
