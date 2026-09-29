export default function (pi) {
  pi.registerCommand("session-identity-probe", {
    description: "Check the current session's generated identity and filename",
    handler: async (_args, ctx) => {
      const id = ctx.sessionManager.getSessionId();
      const file = ctx.sessionManager.getSessionFile() ?? "";
      const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
      const basename = file.split(/[\\/]/).pop();
      const timestamp = /^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z_/;
      ctx.ui.notify(`identity: uuidv7=${uuid.test(id)} filename=${timestamp.test(basename) && basename.endsWith(`_${id}.jsonl`)}`, "info");
    },
  });
}
