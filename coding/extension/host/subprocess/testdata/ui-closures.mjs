import fs from "node:fs";

// ui-closures hands the runtime every UI closure Pi runs only in the TUI; each one that runs appends its name to the file the command is given.
export default function (pi) {
  pi.registerCommand("ui_closures", {
    description: "Hand the runtime footer, header, custom, editor, terminal-input and theme calls",
    handler: async (path, ctx) => {
      const mark = (line) => fs.appendFileSync(path, `${line}\n`);
      ctx.ui.setFooter(() => {
        mark("footer");
        return { render: () => { mark("footer-render"); return ["footer"]; }, invalidate() {} };
      });
      ctx.ui.setHeader(() => {
        mark("header");
        return { render: () => { mark("header-render"); return ["header"]; }, invalidate() {} };
      });
      ctx.ui.setEditorComponent(() => { mark("editor"); return { render: () => ["editor"], handleInput() {}, invalidate() {} }; });
      mark(`getEditorComponent:${typeof ctx.ui.getEditorComponent()}`);
      const unsubscribe = ctx.ui.onTerminalInput(() => mark("terminal-input"));
      mark(`onTerminalInput:${typeof unsubscribe}`);
      unsubscribe();
      ctx.ui.setWorkingMessage("working");
      ctx.ui.setWorkingIndicator({ frames: ["*"] });
      ctx.ui.setWorkingVisible(true);
      ctx.ui.setHiddenThinkingLabel("hidden");
      try {
        mark(`setTheme:${JSON.stringify(ctx.ui.setTheme(ctx.ui.theme))}`);
      } catch (error) {
        mark(`setTheme:threw:${error?.message}`);
      }
    },
  });
}
