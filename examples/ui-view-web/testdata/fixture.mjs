// A fixture of the page tests (bridge_test.go): `/<name> mount <json>` mounts the panel <name> with the given content,
// `/<name> update <json>` replaces it and `/<name> hide` and `/<name> show` hide and show it, so a test draws any view
// through the real carrier. A fixture that answers (fixture-c, fixture-d) also takes two intents of its view: `fx.open` shows the
// mount and `fx.close` hides it, as the handlers of a `whenHidden` and of a `dismiss` intent do.
export default (name, answers = false) => (pi) => {
  let mount;
  if (answers) {
    pi.onViewIntent(async (inv, ctx) => {
      if (inv.intent === "fx.open") mount = (await ctx.ui.showView(mount)).mount ?? mount;
      else if (inv.intent === "fx.close") mount = (await ctx.ui.hideView(mount)).mount ?? mount;
      return { outcome: "done" };
    });
  }
  pi.registerCommand(name, {
    description: `Mount or update the test panel ${name}`,
    handler: async (args, ctx) => {
      const [, verb, json] = /^\s*(\S+)\s*(.*)$/s.exec(args);
      try {
        if (verb === "mount") mount = (await ctx.ui.mountView({ key: name, slot: "panel", ...JSON.parse(json) })).mount;
        else if (verb === "update") mount = (await ctx.ui.updateView(mount, JSON.parse(json))).mount;
        else mount = (await (verb === "hide" ? ctx.ui.hideView(mount) : ctx.ui.showView(mount))).mount ?? mount;
      } catch (err) {
        ctx.ui.notify(`${name}: ${err.code ?? err.message}`, "error");
      }
    },
  });
};
