// Pi 1402973a rpc-mode.ts createExtensionUIContext: setFooter, setHeader, setEditorComponent, onTerminalInput and the working calls are no-ops, custom() resolves undefined, getEditorComponent answers undefined and setTheme answers { success: false } for a Theme object. No factory or handler is ever called.
export default function (pi) {
  pi.registerCommand("ui-closures", {
    handler: async (_args, ctx) => {
      const ran = [];
      ctx.ui.setFooter(() => {
        ran.push("footer");
        return { render: () => { ran.push("footer-render"); return ["FOOTER-FRAME"]; }, invalidate() {} };
      });
      ctx.ui.setHeader(() => {
        ran.push("header");
        return { render: () => { ran.push("header-render"); return ["HEADER-FRAME"]; }, invalidate() {} };
      });
      ctx.ui.setEditorComponent(() => {
        ran.push("editor");
        return { render: () => ["EDITOR-FRAME"], handleInput() {}, invalidate() {} };
      });
      const custom = await ctx.ui.custom((_tui, _theme, _keybindings, done) => {
        ran.push("custom");
        done("closed");
        return { render: () => ["CUSTOM-FRAME"], invalidate() {} };
      });
      const unsubscribe = ctx.ui.onTerminalInput(() => { ran.push("terminal-input"); });
      unsubscribe();
      ctx.ui.setWorkingMessage("working");
      ctx.ui.setWorkingIndicator({ frames: ["*"] });
      ctx.ui.setWorkingVisible(true);
      ctx.ui.setHiddenThinkingLabel("hidden");
      let setTheme;
      try {
        setTheme = ctx.ui.setTheme(ctx.ui.theme);
      } catch (error) {
        setTheme = { threw: String(error?.message) };
      }
      ctx.ui.notify(JSON.stringify({
        ran,
        custom: String(custom),
        editorComponent: typeof ctx.ui.getEditorComponent(),
        onTerminalInput: typeof unsubscribe,
        setTheme,
      }), "info");
    },
  });
}
