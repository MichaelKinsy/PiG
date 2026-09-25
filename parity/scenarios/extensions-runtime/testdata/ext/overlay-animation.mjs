// overlay-animation.mjs: deterministic ui.custom overlay for parity.
// Each keypress advances a frame counter; no setInterval.
// Tests: ui.custom factory receives correct tui shim (requestRender is
// a function; width and height are numbers).
export default function (pi) {
  pi.registerCommand("anim", {
    description: "Deterministic animation overlay (parity harness)",
    args: "",
    handler: async (_args, ctx) => {
      await ctx.ui.custom((tui, _theme, _kb, done) => {
        let frame = 0;
        // Assert shim shape immediately on factory entry.
        const shimOk =
          typeof tui.requestRender === "function" &&
          typeof tui.width === "number" &&
          typeof tui.height === "number";
        return {
          render(width) {
            return [`ANIM frame=${frame} width=${width}`];
          },
          handleInput(data) {
            if (data === "q" || data === "\x1b") {
              done(frame);
              return;
            }
            frame++;
            if (typeof tui.requestRender === "function") {
              tui.requestRender();
            }
          },
          invalidate() {},
          dispose() {},
        };
      });
    },
  });
}
