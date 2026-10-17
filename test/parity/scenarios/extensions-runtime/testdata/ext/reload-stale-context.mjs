// After `await ctx.reload()` the command's captured pi and ctx are stale: Pi invalidates the old runner
// (agent-session.ts:3660-3664) and every member throws its stale message (runner.ts:721-735, loader.ts:161-198).
// /reload-stale records, per member, whether it still answered or threw (and the message's first sentence).
import { appendFileSync } from "node:fs";

const log = (line) => appendFileSync(process.env.RELOAD_STALE_LOG, line + "\n");

export default function (pi) {
  pi.registerCommand("reload-stale", {
    description: "Reload, then probe the captured pi and ctx",
    handler: async (_args, ctx) => {
      await ctx.reload();
      const probes = [
        ["pi.getActiveTools", () => pi.getActiveTools()],
        ["pi.getAllTools", () => pi.getAllTools()],
        ["pi.getCommands", () => pi.getCommands()],
        ["pi.getThinkingLevel", () => pi.getThinkingLevel()],
        ["pi.getFlag", () => pi.getFlag("x")],
        ["ctx.cwd", () => ctx.cwd],
        ["ctx.hasUI", () => ctx.hasUI],
        ["ctx.getSystemPrompt", () => ctx.getSystemPrompt()],
        ["ctx.isProjectTrusted", () => ctx.isProjectTrusted()],
        ["ctx.isIdle", () => ctx.isIdle()],
      ];
      for (const [name, probe] of probes) {
        try {
          probe();
          log(`${name} live`);
        } catch (error) {
          log(`${name} threw: ${String(error?.message ?? error).split(". ")[0]}`);
        }
      }
    },
  });
}
