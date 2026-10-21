// Pi rpc-mode.ts:137-271 writes each UI request with JSON.stringify of a literal object: "", [] and 0 are written as the extension passed them, and only an
// undefined member is omitted. This command makes every such call once, with the empty and the omitted spelling of each optional member, and reports what the dialogs returned.
export default function (pi) {
  pi.registerCommand("empties", {
    handler: async (_args, ctx) => {
      const ui = ctx.ui;
      const results = [];
      const seen = (value) => results.push(value === undefined ? "undefined" : value);
      ui.notify("");
      ui.notify("typed", "warning");
      ui.notify("", "error");
      ui.setStatus("k", "");
      ui.setStatus("k", undefined);
      ui.setStatus("k", "text");
      ui.setTitle("");
      ui.setEditorText("");
      ui.pasteToEditor("");
      // Dialogs with a timeout resolve on their own, as Pi's timer does; timeout 0 is falsy, so Pi arms none and the request waits for the response the scenario sends.
      seen(await ui.select("T", [], { timeout: 50 }));
      seen(await ui.select("", [], { timeout: 50 }));
      seen(await ui.confirm("", "", { timeout: 50 }));
      seen(await ui.input("T", undefined, { timeout: 50 }));
      seen(await ui.input("T", "", { timeout: 50 }));
      seen(await ui.input("T", "p", { timeout: 50 }));
      seen(await ui.select("zero", [], { timeout: 0 }));
      seen(await ui.confirm("zero-confirm", "", { timeout: 0 }));
      seen(await ui.input("zero-input", undefined, { timeout: 0 }));
      seen(await ui.editor("editor-unset"));
      seen(await ui.editor("editor-empty", ""));
      seen(await ui.editor("editor-prefill", "x"));
      ui.notify(`results:${JSON.stringify(results)}`, "info");
    },
  });
}
