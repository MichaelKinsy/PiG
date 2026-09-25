// parity-lifecycle: records session_start and exposes the observed reason via
// a slash command. This avoids relying on transient startup notifications, which
// can disappear on cold extension builds before the parity runner captures.
export default function (pi) {
  let reason = "missing";
  pi.on("session_start", (event, _ctx) => {
    reason = event.reason || "unknown";
  });
  pi.registerCommand("lifecycle-status", {
    description: "Report observed session_start reason.",
    args: "",
    handler: async (_args, ctx) => {
      ctx.ui.notify("lifecycle:session_start:" + reason, "info");
    },
  });
}
