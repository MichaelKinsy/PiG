// timer-overlay.mjs: proves tui.requestRender triggers frame pushes
// independently of handleInput. The overlay sets a 50ms interval that
// increments a counter and calls requestRender(); no input is needed
// for frames to advance. After 3 frames, it auto-closes.
export default function (pi) {
  pi.registerCommand("timer_overlay", {
    description: "Timer-driven overlay that auto-advances frames via requestRender",
    async handler(_args, ctx) {
      let disposed = false;
      const result = await ctx.ui.custom((tui, _theme, _kb, done) => {
        let frame = 0;
        const shimType = typeof tui.requestRender;
        let interval = null;

        // Start a timer that advances frames WITHOUT any handleInput
        interval = setInterval(() => {
          frame++;
          if (typeof tui.requestRender === "function") {
            tui.requestRender(); // THIS is what we're testing
          }
          if (frame >= 3) {
            clearInterval(interval);
            interval = null;
            done({ frames: frame, shimType });
          }
        }, 50);

        return {
          render(width) {
            return [`TIMER frame=${frame} shim=${shimType} width=${width}`];
          },
          handleInput(data) {
            // Early exit on q/Esc
            if (data === "q" || data === "\x1b") {
              if (interval) clearInterval(interval);
              done({ frames: frame, shimType });
            }
          },
          invalidate() {},
          dispose() {
            disposed = true;
            if (interval) { clearInterval(interval); interval = null; }
            queueMicrotask(() => tui.requestRender());
          },
        };
      });
      // Notify with result so integration test can verify
      ctx.ui.notify(`timer-result:${JSON.stringify(result)} disposed=${disposed}`, "info");
    },
  });
}
