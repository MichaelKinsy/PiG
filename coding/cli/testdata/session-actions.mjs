// session-actions.mjs drives the session through the extension API:
// sendUserMessage from agent_end and agent_settled handlers and from a
// command.
export default function sessionActionsExtension(pi) {
  let followUpArmed = false;
  let loopArmed = false;
  let loopDone = false;

  // A loop extension: after the run that answered "loop-start" settles, send
  // the next task.
  pi.on("agent_end", (event) => {
    if (!loopDone && JSON.stringify(event.messages).includes("reply with exactly: loop-start")) {
      loopArmed = true;
    }
  });

  pi.on("agent_settled", () => {
    if (!loopArmed) return;
    loopArmed = false;
    loopDone = true;
    pi.sendUserMessage("reply with exactly: loop-done");
  });

  pi.registerCommand("arm-follow-up", {
    description: "Queue a follow-up from the next agent_end",
    handler: async () => {
      followUpArmed = true;
    },
  });

  pi.on("agent_end", () => {
    if (!followUpArmed) return;
    followUpArmed = false;
    pi.sendUserMessage("reply with exactly: follow-up-ok", { deliverAs: "followUp" });
  });

  pi.registerCommand("start-run", {
    description: "Start a run from the extension while idle",
    handler: async () => {
      pi.sendUserMessage("Run: sleep for a while");
    },
  });
}
