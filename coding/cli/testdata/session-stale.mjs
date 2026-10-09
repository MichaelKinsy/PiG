// session-stale.mjs keeps the pi API and command ctx of the Session that called
// ctx.newSession() and uses them after the replacement. Pi invalidates the
// outgoing runner (agent-session-runtime.ts:167-177), so each use throws the
// runner's stale message. Lines go to the file named by PIG_TEST_STALE_LOG.
import { appendFileSync } from "node:fs";

export default function sessionStaleExtension(pi) {
  const log = (line) => appendFileSync(process.env.PIG_TEST_STALE_LOG, `${line}\n`);
  const attempt = async (name, use) => {
    try {
      await use();
      log(`${name} ok`);
    } catch (error) {
      log(`${name} threw ${error instanceof Error ? error.message : String(error)}`);
    }
  };

  pi.registerCommand("stale-probe", {
    description: "Use pi and ctx after ctx.newSession()",
    handler: async (_args, ctx) => {
      await ctx.newSession();
      await attempt("pi.getActiveTools", () => pi.getActiveTools());
      await attempt("pi.exec", () => pi.exec("echo", ["stale"]));
      await attempt("pi.sendUserMessage", () => pi.sendUserMessage("stale message"));
      // The host applies fire-and-forget calls after later calls of the lane, so flush them.
      await attempt("pi.exec flush", () => pi.exec("echo", ["flush"]));
    },
  });
}
