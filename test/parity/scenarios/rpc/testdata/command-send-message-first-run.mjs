// An extension command starts the session's first run with pi.sendMessage and triggerTurn, as a scheduler extension kicks off its first autonomous run. Pi's sendCustomMessage runs the same _startRun preparation as a prompt, so the run's request carries the system prompt.
export default function (pi) {
  pi.registerCommand("kick", {
    handler: () => {
      pi.sendMessage({ customType: "kick", content: [{ type: "text", text: "Begin the work." }], display: true }, { triggerTurn: true });
    },
  });
}
