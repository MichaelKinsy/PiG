// session-dir.mjs records ctx.sessionManager.getSessionDir() at every session_start. Lines go to the file named by PIG_TEST_REPLACE_LOG.
import { appendFileSync } from "node:fs";

export default function sessionDirExtension(pi) {
  const log = (line) => appendFileSync(process.env.PIG_TEST_REPLACE_LOG, `${line}\n`);
  pi.on("session_start", (event, ctx) => log(`session_start reason=${event.reason} dir=${ctx.sessionManager.getSessionDir()}`));
  pi.registerCommand("replace-new", {
    description: "Replace the Session through ctx.newSession()",
    handler: async (_args, ctx) => {
      await ctx.newSession();
    },
  });
}
