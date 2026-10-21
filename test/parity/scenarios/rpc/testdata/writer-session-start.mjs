// session_start appends an entry, names the session and notifies; /writer-after does the same from a command, after the RPC writer subscribed. The custom message session_start sends is covered by 63-rpc-session-start-message-after-response.
export default function (pi) {
  pi.on("session_start", (event, ctx) => {
    pi.appendEntry("probe-start", { phase: event.reason });
    pi.setSessionName("start-name-" + event.reason);
    ctx.ui.notify("start-notify-" + event.reason, "info");
  });
  pi.registerCommand("writer-after", {
    handler: (_args, ctx) => {
      pi.appendEntry("probe-after", { n: 1 });
      pi.setSessionName("after-name");
      ctx.ui.notify("after-notify", "info");
    },
  });
}
