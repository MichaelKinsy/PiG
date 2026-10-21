// Pi rpc-mode.ts:137-150 and 254-266 resolve a dialog with whatever JSON the client's extension_ui_response carries (`"value" in r ? r.value : undefined`,
// `"confirmed" in r ? r.confirmed : false`, `"cancelled" in r && r.cancelled` by truthiness). This command opens one dialog per answer shape and reports what each returned.
export default function (pi) {
  pi.registerCommand("typed", {
    handler: async (_args, ctx) => {
      const ui = ctx.ui;
      const results = {};
      const seen = (name, value) => { results[name] = value === undefined ? "<undefined>" : value; };
      seen("select-number", await ui.select("select-number", ["a"]));
      seen("select-null", await ui.select("select-null", ["a"]));
      seen("select-object", await ui.select("select-object", ["a"]));
      seen("select-absent", await ui.select("select-absent", ["a"]));
      seen("select-cancelled-truthy", await ui.select("select-cancelled-truthy", ["a"]));
      seen("select-cancelled-falsy", await ui.select("select-cancelled-falsy", ["a"]));
      seen("confirm-string", await ui.confirm("confirm-string", "m"));
      seen("confirm-null", await ui.confirm("confirm-null", "m"));
      seen("confirm-zero", await ui.confirm("confirm-zero", "m"));
      seen("confirm-absent", await ui.confirm("confirm-absent", "m"));
      seen("input-array", await ui.input("input-array", "p"));
      seen("editor-number", await ui.editor("editor-number", "p"));
      seen("custom", await ui.custom(() => ({ render: () => [], invalidate() {} })));
      ui.notify(`results:${JSON.stringify(results)}`, "info");
    },
  });
}
