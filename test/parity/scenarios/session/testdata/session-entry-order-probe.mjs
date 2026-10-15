export default function (pi) {
  pi.registerCommand("session-entry-order-probe", {
    description: "List the current session's entry types and name",
    handler: async (_args, ctx) => {
      const types = ctx.sessionManager.getEntries().map((entry) => entry.type).join(",");
      ctx.ui.notify(`entries: ${types} name=${ctx.sessionManager.getSessionName() ?? ""}`, "info");
    },
  });
}
