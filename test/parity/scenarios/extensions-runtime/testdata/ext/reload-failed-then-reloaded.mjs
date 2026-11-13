// A /reload that fails, then one that loads: what each extension instance's captured pi and ctx answer.
// Pi invalidates the old runner before the step of reload() that can throw and builds the new one after it
// (agent-session.ts:3825-3826, 3838, 3844), and the interactive mode catches the throw (interactive-mode.ts:6670-6704).
// The process and its globals outlive a reload, so the instances share the cells and each keeps its own probe.
import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { getAgentDir } from "@earendil-works/pi-coding-agent";

const state = (globalThis.__parityReloadCells ??= { cells: [], probes: [], generations: 0 });
const answer = (call) => {
  try {
    call();
    return "live";
  } catch (error) {
    return "threw: " + String(error?.message ?? error).split(". ")[0];
  }
};

export default function (pi) {
  const g = "g" + ++state.generations;
  state.probes.push([g, () => answer(() => pi.getActiveTools())]);
  pi.on("session_start", async (event) => {
    state.cells.push(`${g} session_start ${event.reason}`);
  });
  pi.on("session_shutdown", async (event) => {
    state.cells.push(`${g} session_shutdown ${event.reason}`);
  });
  pi.registerCommand("parity-reload-fails", {
    description: "Reload with a settings entry the resource loader rejects, then use the captured pi and ctx",
    handler: async (_args, ctx) => {
      const path = join(getAgentDir(), "settings.json");
      const original = readFileSync(path, "utf8");
      writeFileSync(path, JSON.stringify({ ...JSON.parse(original), prompts: ["file:///a%2Fb"] }));
      try {
        await ctx.reload();
        state.cells.push(`${g} ctx.reload() resolved`);
      } catch (error) {
        state.cells.push(`${g} ctx.reload() rejected: ${String(error?.message ?? error).split(". ")[0]}`);
      }
      writeFileSync(path, original);
      state.cells.push(`${g} pi after the failed reload ${answer(() => pi.getActiveTools())}`);
      state.cells.push(`${g} ctx after the failed reload ${answer(() => ctx.cwd)}`);
    },
  });
  pi.registerCommand("parity-reload-cells", {
    description: "Ask every instance's captured pi and show the cells",
    handler: async (_args, ctx) => {
      for (const [generation, probe] of state.probes) state.cells.push(`${generation} pi after the second reload ${probe()}`);
      ctx.ui.notify(["reload-cells-begin", ...state.cells, "reload-cells-end"].join("\n"), "info");
    },
  });
}
