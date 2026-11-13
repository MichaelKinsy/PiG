// Fixture of the plugin UI carrier tests (D614): commands that mount and change views, and the intents they answer.
import { writeFileSync } from "node:fs";

export default function rpcUIViewExtension(pi) {
  const text = (value) => ({ root: { kind: "text", text: value } });
  const intents = [
    { name: "fx.add", arg: { type: "object", properties: { n: { type: "integer" } }, required: ["n"] }, answer: { type: "object", properties: { sum: { type: "integer" } }, required: ["sum"] } },
    { name: "fx.wait", arg: { type: "object", properties: { tag: { type: "string" } }, required: ["tag"] }, cancellable: true, timeoutMs: 60000 },
  ];
  let shown;

  // view-try reports what a mount returned. With PIG_TEST_VIEW_MOUNT unset it makes no view call at all and registers no
  // abilities handler, so two sessions that differ only in that variable differ only in the reported code.
  pi.registerCommand("view-try", {
    description: "Report the result of a mount",
    handler: async (_args, ctx) => {
      let code = "none";
      if (process.env.PIG_TEST_VIEW_MOUNT === "1") {
        try {
          await ctx.ui.mountView({ key: "try", slot: "panel", view: text("try") });
          code = "mounted";
        } catch (err) {
          code = err.code;
        }
      }
      ctx.ui.notify(`view-result:${code}`, "info");
    },
  });

  pi.registerCommand("view-show", {
    description: "Mount a view with two intents",
    handler: async (_args, ctx) => {
      const st = await ctx.ui.mountView({ key: "show", slot: "panel", view: text("shown-0"), intents });
      shown = st.mount;
      ctx.ui.notify(`view-shown:epoch=${st.mount.epoch},rev=${st.mount.rev}`, "info");
    },
  });

  // view-burst <n> replaces the content of the mount n times.
  pi.registerCommand("view-burst", {
    description: "Update the mounted view n times",
    handler: async (args, ctx) => {
      const n = Number.parseInt(args, 10);
      for (let i = 1; i <= n; i++) shown = (await ctx.ui.updateView(shown, { view: text(`shown-${i}`), intents })).mount;
      // A client that stopped reading cannot see the command end, so the end is also written to a file.
      if (process.env.PIG_TEST_VIEW_DONE) writeFileSync(process.env.PIG_TEST_VIEW_DONE, String(shown.rev));
      ctx.ui.notify(`view-burst:rev=${shown.rev}`, "info");
    },
  });

  // view-redraw mounts a panel whose intent is answered by a handler that redraws the panel first.
  const redraw = [{ name: "fx.redraw", arg: { type: "object", properties: { n: { type: "integer" } }, required: ["n"] }, answer: { type: "object", properties: { rev: { type: "integer" } }, required: ["rev"] } }];
  pi.registerCommand("view-redraw", {
    description: "Mount a view whose intent redraws it",
    handler: async (_args, ctx) => {
      await ctx.ui.mountView({ key: "redraw", slot: "panel", view: text("redrawn-0"), intents: redraw });
    },
  });

  // view-reload reloads the session's extensions, as /reload does in the terminal.
  pi.registerCommand("view-reload", {
    description: "Reload the extensions",
    handler: async (_args, ctx) => {
      await ctx.reload();
    },
  });

  // The abilities handler. PIG_TEST_VIEW_ARRIVE=mount mounts a view for the client that arrived; =block never returns.
  // With PIG_TEST_VIEW_MOUNT=1 alone it is registered and, in a session no client attaches to, never runs.
  const arrive = process.env.PIG_TEST_VIEW_ARRIVE;
  if (arrive || process.env.PIG_TEST_VIEW_MOUNT === "1") {
    pi.onViewAbilities(async (abilities, ctx) => {
      ctx.ui.notify(`view-arrive:${abilities ? "client" : "none"}`, "info");
      if (!abilities) return;
      if (arrive === "block") await new Promise(() => {});
      if (arrive === "mount") await ctx.ui.mountView({ key: "arrived", slot: "panel", view: text(`arrived-${ctx.ui.viewAbilities().limits.intentArgBytes}-${abilities.slots.join("+")}`) });
    });
  }

  pi.registerCommand("view-notices", {
    description: "Report the notices of a mounted view (the argument names its key; show by default)",
    handler: async (args, ctx) => {
      const notices = await ctx.ui.viewDiagnostics(args.trim() || "show");
      ctx.ui.notify(`view-notices:${notices.map((n) => `${n.code}/${n.severity}/${n.message ?? ""}`).join(",")}`, "info");
    },
  });

  pi.onViewIntent(async (inv, ctx) => {
    if (inv.intent === "fx.redraw") {
      // Change, redraw, answer: the update is made inside the answer, with no delay.
      const st = await ctx.ui.updateView({ ...inv.mount, rev: inv.rev }, { view: text(`redrawn-${inv.arg.n}`), intents: redraw });
      return { outcome: "done", answer: { rev: st.mount.rev } };
    }
    if (inv.intent === "fx.add") return { outcome: "done", answer: { sum: inv.arg.n + (ctx.ui.viewAbilities()?.limits.intentArgBytes ?? 0) } };
    if (inv.intent === "fx.wait") {
      ctx.ui.notify(`intent-started:${inv.arg.tag}`, "info");
      if (!inv.signal.aborted) await new Promise((resolve) => inv.signal.addEventListener("abort", resolve, { once: true }));
      return { outcome: "done", answer: {} };
    }
    return { outcome: "refused", error: "unknown intent" };
  });
}
