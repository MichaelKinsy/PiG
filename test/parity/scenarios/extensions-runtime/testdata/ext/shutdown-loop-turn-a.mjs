// The first of two extensions in a print-mode disposal. Its session_shutdown handler queues an
// immediate and a zero-delay timer, and each records whether the ctx is still live when it runs.
// Pi's runner awaits each extension's handler in turn on one event loop (runner.ts:1089-1116), so
// when shutdown-loop-turn-b.mjs awaits a timer, these callbacks run before the runner is invalidated.
import { appendFileSync } from "node:fs";

export default function (pi) {
  const record = (line) => appendFileSync(process.env.LOOP_TURN_LOG, `${line}\n`);
  const state = (ctx) => {
    try {
      ctx.cwd;
      return "live";
    } catch (error) {
      return String(error?.message).includes("stale") ? "stale" : `threw ${error?.message}`;
    }
  };
  pi.on("session_shutdown", (_event, ctx) => {
    record("a session_shutdown");
    setImmediate(() => record(`a immediate ${state(ctx)}`));
    setTimeout(() => record(`a timeout ${state(ctx)}`), 0);
  });
}
