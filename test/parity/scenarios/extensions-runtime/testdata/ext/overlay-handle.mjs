export default function (pi) {
  pi.on("session_start", (_event, ctx) => ctx.ui.setStatus("node-handle-ready", "NODE HANDLE READY"));
  pi.registerCommand("node-handle", {
    description: "Observe mounted overlay controls and measured stdout geometry",
    async handler(_args, ctx) {
      let handle;
      let trace = [];
      let keys = 0;
      await ctx.ui.custom((tui, _theme, _keys, done) => ({
        focused: false,
        render(width) {
          const text = handle ? `NODE-HANDLE focus=${handle.isFocused()} hidden=${handle.isHidden()} geometry=${process.stdout.columns}x${process.stdout.rows} trace=${trace.join(",")} keys=${keys}` : "NODE-HANDLE mounting";
          return [text.padEnd(width)];
        },
        handleInput(data) {
          if (data === "q") done();
          else { keys++; tui.requestRender(); }
        },
      }), {
        overlay: true,
        overlayOptions: { width: "100%", anchor: "top-left", row: 0, col: 0, nonCapturing: true },
        onHandle(value) {
          handle = value;
          trace.push(handle.isFocused());
          handle.focus(); trace.push(handle.isFocused());
          handle.unfocus(); trace.push(handle.isFocused());
          handle.setHidden(true); trace.push(handle.isHidden());
          handle.setHidden(false); handle.focus();
        },
      });
    },
  });
}
