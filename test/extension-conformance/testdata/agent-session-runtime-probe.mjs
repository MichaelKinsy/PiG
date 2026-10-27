// The probe extension of the agent_session_runtime row (agent_session_runtime_test.go): every session the runtime builds loads it. It appends one line per session event to runtime-events.log in the session's cwd, and cancels a session_before_switch or session_before_fork while a file named cancel-next exists there.
import { appendFileSync, existsSync } from "node:fs";
import { join } from "node:path";

export default function (pi) {
  const log = (ctx, parts) => appendFileSync(join(ctx.cwd, "runtime-events.log"), parts.join("|") + "\n");
  const cancelling = (ctx) => existsSync(join(ctx.cwd, "cancel-next"));
  pi.on("session_start", async (event, ctx) => log(ctx, ["start", event.reason, event.previousSessionFile ? "previous" : "-"]));
  pi.on("session_before_switch", async (event, ctx) => {
    log(ctx, ["before_switch", event.reason, event.targetSessionFile ? "target" : "-"]);
    if (cancelling(ctx)) return { cancel: true };
  });
  pi.on("session_before_fork", async (event, ctx) => {
    log(ctx, ["before_fork", event.position, "-"]);
    if (cancelling(ctx)) return { cancel: true };
  });
  pi.on("session_shutdown", async (event, ctx) => log(ctx, ["shutdown", event.reason, event.targetSessionFile ? "target" : "-"]));
}
