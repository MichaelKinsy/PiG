import { appendFileSync } from "node:fs";

// Reads ctx.signal from a timer, with no request reaching this extension after session_start, and records each change of state as a JSON line in CTX_SIGNAL_POLL_REPORT.
// Pi: ctx.signal is a getter over `agent.signal` (runner.ts:917-920, agent-session.ts:3368, agent.ts:336-338), so the timer sees a run begin and end the moment it does.
export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    let last;
    const poll = () => {
      const state = ctx.signal === undefined ? "undefined" : "live";
      if (state === last) return;
      last = state;
      appendFileSync(process.env.CTX_SIGNAL_POLL_REPORT, JSON.stringify({ where: "poll", signal: state }) + "\n");
    };
    // The first read is the handler's own, before any run can begin.
    poll();
    setInterval(poll, 5).unref();
  });
}
