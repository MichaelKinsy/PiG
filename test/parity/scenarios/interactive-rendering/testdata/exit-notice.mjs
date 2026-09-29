export default function (pi) {
  pi.registerCommand("exit-notice", {
    description: "Emit the exit teardown regression notice",
    handler: async (args, ctx) => {
      if (args === "probe") ctx.ui.notify("NOTICE-BEGIN", "warning");
      ctx.ui.notify("chain finished: 4 steps in 4.6s", "info");
    },
  });
}
