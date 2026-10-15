// session-switch.mjs switches to the Session file named by the command argument
// through ctx.switchSession() and records the outcome. Lines go to the file
// named by PIG_TEST_REPLACE_LOG.
import { appendFileSync } from "node:fs";

export default function sessionSwitchExtension(pi) {
  const log = (line) => appendFileSync(process.env.PIG_TEST_REPLACE_LOG, `${line}\n`);
  pi.on("session_start", (event) => log(`session_start reason=${event.reason}`));
  pi.registerCommand("switch-to", {
    description: "Switch Session through ctx.switchSession()",
    handler: async (args, ctx) => {
      const result = await ctx.switchSession(args.trim());
      log(`switch-to cancelled=${result.cancelled}`);
    },
  });
}
