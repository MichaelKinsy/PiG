export default function (pi) {
  pi.registerCommand("burst_overlay", {
    description: "Burst timer redraws are coalesced before IPC",
    async handler(_args, ctx) {
      const result = await ctx.ui.custom((tui, _theme, _keybindings, done) => {
        let renders = 0;
        queueMicrotask(() => {
          for (let i = 0; i < 10000; i++) tui.requestRender();
          setTimeout(() => done({ renders }), 40);
        });
        return {
          render(width) {
            renders++;
            return [`BURST renders=${renders} width=${width}`];
          },
          handleInput() {},
        };
      });
      ctx.ui.notify(`burst-result:${JSON.stringify(result)}`, "info");
    },
  });
}
