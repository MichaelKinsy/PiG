import { basename, isAbsolute } from "node:path";

export default function (pi) {
  const report = (ctx, event) => {
    const id = ctx.sessionManager.getSessionId();
    const file = ctx.sessionManager.getSessionFile() || "";
    ctx.ui.notify(`identity:${event}:${id}:${basename(file)}:absolute=${isAbsolute(file)}`, "info");
  };

  pi.on("session_start", (event, ctx) => report(ctx, `session_start-${event.reason}`));
  pi.on("agent_start", (_event, ctx) => report(ctx, "agent_start"));
  pi.registerCommand("identity-probe", {
    description: "Report the current extension session identity.",
    handler: async (_args, ctx) => report(ctx, "command"),
  });
}
