// session-cwd-probe.mjs reports, on every session_start, the working directory and
// the project context files the Session's system prompt carries. The lines go to
// the file named by PIG_TEST_CWD_LOG.
import { appendFileSync } from "node:fs";

export default function sessionCwdProbe(pi) {
  pi.on("session_start", (event, ctx) => {
    const prompt = ctx.getSystemPrompt();
    const markers = ["STARTUP-RULES", "DESTINATION-RULES"].filter((marker) => prompt.includes(marker)).join("+");
    appendFileSync(process.env.PIG_TEST_CWD_LOG, `session_start reason=${event.reason} cwd=${ctx.cwd} context=${markers || "none"}\n`);
  });
}
