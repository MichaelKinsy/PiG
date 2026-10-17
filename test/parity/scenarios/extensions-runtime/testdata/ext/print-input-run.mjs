// Records whether the callback a print prompt's input handler queues runs during the run the input goes on to.
// The input continues, so Pi's prompt() goes on to the agent, whose bash tool I/O turns the event loop (agent-session.ts:1968-2000), and the immediate runs before session_shutdown.
import { appendFileSync } from "node:fs";

export default function (pi) {
  const record = (line) => appendFileSync(process.env.PRINT_INPUT_RUN_LOG, `${line}\n`);
  pi.on("input", (event) => {
    record(`input ${event.text}`);
    setImmediate(() => record("input-immediate"));
  });
  pi.on("session_shutdown", () => record("session_shutdown"));
}
