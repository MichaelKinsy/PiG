import { existsSync, readFileSync, writeFileSync, unlinkSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { getAgentDir } from "@earendil-works/pi-coding-agent";

export default function (pi) {
  pi.registerCommand("pend-custom", {
    description: "Await a custom surface while a reload starts",
    handler: async (_args, ctx) => {
      setTimeout(() => { void ctx.reload(); }, 800);
      await ctx.ui.custom((_tui, _theme, _keys, done) => ({ render: (w) => ["PEND-CUSTOM-OVERLAY".padEnd(w)], invalidate() {}, handleInput(d) { if (d === "q") done(); } }), { overlay: true });
      writeFileSync(join(getAgentDir(), "late-custom.marker"), "late code ran");
    },
  });
  pi.registerCommand("pend-select", {
    description: "Await a select surface while a reload starts",
    handler: async (_args, ctx) => {
      setTimeout(() => { void ctx.reload(); }, 800);
      await ctx.ui.select("PEND-SELECT-TITLE", ["one", "two"]);
      writeFileSync(join(getAgentDir(), "late-select.marker"), "late code ran");
    },
  });
  pi.registerCommand("pend-confirm", {
    description: "Await a confirm surface while a reload starts",
    handler: async (_args, ctx) => {
      setTimeout(() => { void ctx.reload(); }, 800);
      await ctx.ui.confirm("PEND-CONFIRM-TITLE", "PEND-CONFIRM-BODY");
      writeFileSync(join(getAgentDir(), "late-confirm.marker"), "late code ran");
    },
  });
  pi.registerCommand("pend-input", {
    description: "Await a input surface while a reload starts",
    handler: async (_args, ctx) => {
      setTimeout(() => { void ctx.reload(); }, 800);
      await ctx.ui.input("PEND-INPUT-TITLE", "type here");
      writeFileSync(join(getAgentDir(), "late-input.marker"), "late code ran");
    },
  });
  pi.registerCommand("pend-editor", {
    description: "Await a editor surface while a reload starts",
    handler: async (_args, ctx) => {
      setTimeout(() => { void ctx.reload(); }, 800);
      await ctx.ui.editor("PEND-EDITOR-TITLE", "prefill");
      writeFileSync(join(getAgentDir(), "late-editor.marker"), "late code ran");
    },
  });
}
