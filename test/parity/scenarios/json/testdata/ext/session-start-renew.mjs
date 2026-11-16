// The extension of Pi's test/suite/print-mode-session-start-events.test.ts: session_start appends an entry named for its reason, and /renew replaces the session.
export default function (pi) {
  pi.on("session_start", (event) => {
    pi.appendEntry("start-marker", event.reason);
  });
  pi.registerCommand("renew", {
    description: "start a new session",
    handler: async (_args, ctx) => {
      await ctx.newSession();
    },
  });
}
