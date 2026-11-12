// A fixture of the page tests (bridge_test.go): `/<name> mount <json>` mounts the panel <name> with the given content
// and `/<name> update <json>` replaces it, so a test draws any view through the real carrier.
export default (name) => (pi) => {
  let mount;
  pi.registerCommand(name, {
    description: `Mount or update the test panel ${name}`,
    handler: async (args, ctx) => {
      const [, verb, json] = /^\s*(\S+)\s+(.*)$/s.exec(args);
      try {
        if (verb === "mount") mount = (await ctx.ui.mountView({ key: name, slot: "panel", ...JSON.parse(json) })).mount;
        else mount = (await ctx.ui.updateView(mount, JSON.parse(json))).mount;
      } catch (err) {
        ctx.ui.notify(`${name}: ${err.code ?? err.message}`, "error");
      }
    },
  });
};
