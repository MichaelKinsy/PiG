// Pi 18e99051be24 (#12233): a pi.events handler that throws is reported to the extension error listener like a pi.on handler's error, with the
// extension's path, event `events:<channel>` and the stack, and nothing is written behind the TUI. The stack is fixed so both hosts show the same frames.
export default function (pi) {
  pi.events.on("demo:boom", () => {
    const error = new Error("demo handler failed");
    error.stack = "Error: demo handler failed\n    at demoFrame (demo.mjs:1:1)\n  events-last-frame";
    throw error;
  });
  pi.registerCommand("events-boom", {
    description: "Emit demo:boom, whose handler throws",
    handler: async () => {
      pi.events.emit("demo:boom", { n: 1 });
    },
  });
}
