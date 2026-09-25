// Reports what the runtime believes the session log and branch are, so an
// incremental push can be checked against the host's own view.
export default function (pi) {
  pi.registerCommand("report_session", {
    description: "Report replicated entry ids and the derived branch",
    handler: async (_args, ctx) => {
      const ids = ctx.sessionManager.getEntries().map((e) => e.id).join(",");
      const branch = ctx.sessionManager.getBranch().map((e) => e.id).join(",");
      ctx.ui.notify(`entries=[${ids}] branch=[${branch}]`, "info");
    },
  });
}
