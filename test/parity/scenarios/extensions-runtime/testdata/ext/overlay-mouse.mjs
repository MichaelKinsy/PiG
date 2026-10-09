// A ui.custom overlay whose component takes the mouse (Pi's handleMouse) and
// draws every event it receives, so a fullscreen click, double click, wheel
// and modified click show their routed fields in the overlay's rows.
export default function (pi) {
  pi.on("session_start", (_event, ctx) => {
    ctx.ui.setStatus("mouse-probe-ready", "MOUSE-PROBE-READY");
  });
  pi.registerCommand("mouse", {
    description: "Open an overlay that logs mouse events",
    handler: async (_args, ctx) => {
      ctx.ui.setStatus("mouse-probe-ready", undefined);
      const log = [];
      await ctx.ui.custom(
        (_tui, _theme, _kb, done) => ({
          render(width) {
            const rows = ["MOUSE TOP", ...log.slice(-10)];
            while (rows.length < 11) rows.push("-");
            rows.push("MOUSE END");
            return rows.map((row) => `|${row}`.padEnd(width).slice(0, width));
          },
          handleInput(data) {
            if (data === "\x1b") done(undefined);
          },
          handleMouse(event) {
            const mods = `${event.shift ? "S" : ""}${event.alt ? "A" : ""}${event.ctrl ? "C" : ""}`;
            log.push(
              `${event.type} ${event.button} ${event.x},${event.y} ${event.screenX},${event.screenY} ` +
                `${event.width}x${event.height} c${event.clickCount ?? "-"} w${event.wheelDelta ?? "-"} ${mods || "-"}`,
            );
            // Take the press, so the release and click follow it (tui-alt-screen.ts handleMouseEvent).
            return event.type === "press" || event.type === "wheel" ? { handled: true } : undefined;
          },
          invalidate() {},
        }),
        { overlay: true, overlayOptions: { anchor: "top-left", row: 3, col: 0, width: 60 } },
      );
    },
  });
}
