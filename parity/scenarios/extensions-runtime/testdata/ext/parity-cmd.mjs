// parity-cmd: registers a slash command "/parity-ping" that sends a
// deterministic message. Exercises loader.ts discovery + sdk.ts registration.
export default function (pi) {
  pi.registerCommand("parity-ping", {
    description: "Parity test command that replies with PONG.",
    args: "",
    handler: async (args, ctx) => {
      ctx.ui.notify("PONG", "info");
    },
  });
}
