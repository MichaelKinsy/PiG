// Exercises ExtensionAPI and ui members the Node runtime did not expose while
// the host implemented them and the Go, Rust, and Python SDKs called them.
export default function (pi) {
  pi.registerCommand("exercise_api", {
    description: "Call the previously missing api members",
    handler: async (_args, ctx) => {
      pi.setSessionName("renamed-by-extension");
      pi.setLabel("entry-7", "bookmark");
      ctx.ui.setWorkingVisible(false);
      ctx.ui.notify("done", "info");
    },
  });
}
