// writer-start: session_start appends an entry, names the session and sends a custom message; /writer-after does the same from a command. Written once per SDK (node, go, python, rust); the mode's writer must print the command's events and none of session_start's.
export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    pi.appendEntry("writer-start", { text: "from-start" });
    pi.setSessionName("start-name");
    pi.sendMessage({ customType: "writer-start", content: [{ type: "text", text: "start-message" }], display: false }, { triggerTurn: false });
    ctx.ui.setStatus("writer", "writer: ready");
  });
  pi.registerCommand("writer-after", {
    description: "Append, name and send from a command",
    handler: async (_args, _ctx) => {
      pi.appendEntry("writer-after", { text: "from-command" });
      pi.setSessionName("after-name");
      pi.sendMessage({ customType: "writer-after", content: [{ type: "text", text: "after-message" }], display: false }, { triggerTurn: false });
    },
  });
}
