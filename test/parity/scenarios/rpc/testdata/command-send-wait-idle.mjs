// An extension command sends a user message and then awaits the Session's idle state, as a Pigpen warden's /warden init does. Pi's waitForIdle returns at once while the run the message starts has not begun (agent-session.ts waitForIdle: `if (this.isIdle) return`), so the command returns and the prompt's response is written before agent_start.
export default function (pi) {
  pi.registerCommand("ask", {
    description: "Send a user message and wait for idle",
    handler: async (_args, ctx) => {
      pi.sendUserMessage("Say hello");
      await ctx.waitForIdle();
    },
  });
}
