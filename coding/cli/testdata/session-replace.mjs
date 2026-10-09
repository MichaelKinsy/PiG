// session-replace.mjs records its own instance identity with every lifecycle
// event, so a test can tell whether a replacement Session got fresh extension
// instances (Pi builds them with the Session factory) or kept the old ones.
// Lines go to the file named by PIG_TEST_REPLACE_LOG.
import { randomUUID } from "node:crypto";
import { appendFileSync } from "node:fs";

export default function sessionReplaceExtension(pi) {
  const instance = randomUUID().slice(0, 8);
  const log = (line) => appendFileSync(process.env.PIG_TEST_REPLACE_LOG, `${line}\n`);

  pi.on("session_start", (event) => {
    log(`session_start reason=${event.reason} instance=${instance} previous=${event.previousSessionFile ? "yes" : "no"}`);
  });
  pi.on("session_shutdown", (event) => {
    log(`session_shutdown reason=${event.reason} instance=${instance} target=${event.targetSessionFile ? "yes" : "no"}`);
  });

  pi.registerCommand("replace-new", {
    description: "Replace the Session through ctx.newSession()",
    handler: async (_args, ctx) => {
      const result = await ctx.newSession();
      log(`replace-new cancelled=${result.cancelled} instance=${instance}`);
    },
  });
}
