import { existsSync, readFileSync, writeFileSync, unlinkSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { getAgentDir } from "@earendil-works/pi-coding-agent";

export default function (pi) {
  pi.registerCommand("pend-editor", {
    description: "Await a editor surface while a reload starts",
    handler: async (_args, ctx) => {
      setTimeout(() => { unlinkSync(fileURLToPath(import.meta.url)); void ctx.reload(); }, 800);
      await ctx.ui.editor("PEND-EDITOR-TITLE", "prefill");
      writeFileSync(join(getAgentDir(), "late-editor.marker"), "late code ran");
    },
  });
}
