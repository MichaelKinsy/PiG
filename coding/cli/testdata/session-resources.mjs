// session-resources.mjs records the resources_discover reasons an extension
// receives. Pi's AgentSession.extendResourcesFromExtensions passes "reload" for
// a reload and "startup" for every other Session start, including a Session
// created by a replacement (agent-session.ts:2941). Lines go to the file named
// by PIG_TEST_REPLACE_LOG.
import { appendFileSync } from "node:fs";

export default function sessionResourcesExtension(pi) {
  const log = (line) => appendFileSync(process.env.PIG_TEST_REPLACE_LOG, `${line}\n`);
  pi.on("session_start", (event) => log(`session_start reason=${event.reason}`));
  pi.on("resources_discover", (event) => {
    log(`resources_discover reason=${event.reason}`);
    return {};
  });
  pi.registerCommand("replace-new", {
    description: "Replace the Session through ctx.newSession()",
    handler: async (_args, ctx) => {
      await ctx.newSession();
    },
  });
}
