// The view extension of the view endpoint's reload row (test/integration). It runs in a Node process of its own, and
// every text it draws names that process, the edition of this file (the row rewrites the line below) and how many
// times the process loaded the extension, so the row reads which process and which load of the extension drew a mount
// and answered an intent. It keeps no reference of its own: the handler updates the mount its invocation names.
const EDITION = "one";

export default function viewsub(pi) {
  const load = (globalThis.viewsubLoads = (globalThis.viewsubLoads ?? 0) + 1);
  const content = (title) => ({
    view: { root: { kind: "text", text: `${title} by ${process.pid} edition ${EDITION} load ${load}` } },
    intents: [
      {
        name: "viewsub.pick",
        timeoutMs: 5000,
        arg: { type: "object", properties: { value: { type: "string" } }, required: ["value"] },
        answer: { type: "object", properties: { pid: { type: "integer" }, load: { type: "integer" } }, required: ["pid", "load"] },
      },
    ],
  });

  pi.registerCommand("smount", {
    description: "Mount the panel of the subprocess view fixture",
    handler: async (_args, ctx) => {
      try {
        const st = await ctx.ui.mountView({ key: "panel", slot: "panel", ...content("sub panel") });
        ctx.ui.notify(`viewsub smount: epoch ${st.mount.epoch} rev ${st.mount.rev} ${st.state}`, "info");
      } catch (err) {
        ctx.ui.notify(`viewsub smount: ${err.code ?? err.message}`, "error");
      }
    },
  });

  pi.onViewIntent(async (inv, ctx) => {
    await ctx.ui.updateView({ key: inv.mount.key, epoch: inv.mount.epoch, rev: inv.rev }, content(`picked ${inv.arg.value}`));
    return { outcome: "done", answer: { pid: process.pid, load } };
  });
}
