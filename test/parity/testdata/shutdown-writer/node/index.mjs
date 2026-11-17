// shutdown-writer: session_shutdown appends an entry, names the session, sends a custom message and labels the leaf. Written once per SDK (node, go; the Go SDK has no leaf read, so it does not label). Pi has already unsubscribed the JSON writer when it disposes the runtime, so none of it reaches --mode json stdout; each is still persisted in the session file.
export default function (pi) {
  pi.on("session_shutdown", (event, ctx) => {
    pi.appendEntry("shutdown-entry", { text: "from-shutdown", reason: event.reason });
    pi.setSessionName("shutdown-name");
    pi.sendMessage({ customType: "shutdown-message", content: [{ type: "text", text: "shutdown-text" }], display: false }, { triggerTurn: false });
    const leaf = ctx.sessionManager.getLeafId();
    if (leaf) pi.setLabel(leaf, "shutdown-label");
  });
}
