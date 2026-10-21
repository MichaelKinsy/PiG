// session_start does everything a writer could echo: an appended entry, a session name, a custom message and a notification. /writer-after does the same from a command, after the mode's writer subscribed.
export default function (pi) {
  pi.on("session_start", (event, ctx) => {
    pi.appendEntry("probe-start", { phase: event.reason });
    pi.setSessionName("start-name");
    pi.sendMessage({ customType: "probe", content: [{ type: "text", text: "start-message" }], display: false, details: { phase: event.reason } }, { triggerTurn: false });
    ctx.ui.notify("start-notify", "info");
  });
  pi.registerCommand("writer-after", {
    handler: (_args, ctx) => {
      pi.appendEntry("probe-after", { n: 1 });
      pi.setSessionName("after-name");
      pi.sendMessage({ customType: "probe", content: [{ type: "text", text: "after-message" }], display: false, details: { n: 1 } }, { triggerTurn: false });
      ctx.ui.notify("after-notify", "info");
    },
  });
}
