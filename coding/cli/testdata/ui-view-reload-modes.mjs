// Fixture of the reload rule in the modes that have no view client (D614): each command appends what its view call returned to the file named by PIG_TEST_VIEW_LOG.
import { appendFileSync } from "node:fs";

export default function uiViewReloadModes(pi) {
  const text = (value) => ({ root: { kind: "text", text: value } });
  const log = (line) => appendFileSync(process.env.PIG_TEST_VIEW_LOG, line + "\n");
  const mount = async (ctx, name) => {
    try {
      const st = await ctx.ui.mountView({ key: "k", slot: "panel", view: text(name) });
      log(`${name}:mounted epoch=${st.mount.epoch} state=${st.state}`);
    } catch (err) {
      log(`${name}:${err.code}`);
    }
  };
  pi.registerCommand("view-first", { description: "Mount a view", handler: async (_args, ctx) => mount(ctx, "first") });
  pi.registerCommand("view-again", { description: "Mount the same key again", handler: async (_args, ctx) => mount(ctx, "again") });
  pi.registerCommand("view-reload", { description: "Reload the extensions", handler: async (_args, ctx) => { log("reload"); await ctx.reload(); } });
}
