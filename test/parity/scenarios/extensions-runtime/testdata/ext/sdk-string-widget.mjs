// A string[] widget wider than the pane, as a Go or Python SDK extension sets
// it with ctx.SetWidget(key, lines). Pi wraps it with Text(line, 1, 0).
export default function (pi) {
  pi.on("session_start", async (_event, ctx) => {
    if (!ctx.hasUI) return;
    ctx.ui.setWidget("sdk-wide", ["WIDGET-START " + "W".repeat(150) + " tail words wrap here", "short entry"]);
  });
}
