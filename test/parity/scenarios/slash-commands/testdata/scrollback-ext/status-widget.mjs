// Sets a footer status and a widget above the editor when the session starts, as an extension that reports its state does.
export default function (pi) {
  pi.on("session_start", async (_event, ctx) => {
    ctx.ui.setStatus("sb", "SB-STATUS-TEXT");
    ctx.ui.setWidget("sb", ["SB-WIDGET-LINE-1", "SB-WIDGET-LINE-2"]);
  });
}
