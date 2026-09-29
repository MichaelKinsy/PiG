// Reads the branch before any other session read, as a session_start handler of a
// freshly started extension does after a Session replacement.
export default function (pi) {
  pi.registerCommand("branch_first", {
    description: "Report the branch as the first session read",
    handler: async (_args, ctx) => {
      ctx.ui.notify(`branch=[${ctx.sessionManager.getBranch().map((e) => e.id).join(",")}]`, "info");
    },
  });
}
