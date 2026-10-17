// Records the order of an input an extension sends during a print-mode run and the callback its handler queues.
// pi.sendUserMessage with deliverAs runs the input handlers while the run goes on (agent-session.ts:1968-2000, 2365-2394): prompt() queues the follow-up and returns, and the agent keeps doing I/O, here the bash tool's child process, so the immediate the input handler queued runs before tool_result.
import { appendFileSync } from "node:fs";

export default function (pi) {
  const record = (line) => appendFileSync(process.env.MIDRUN_INPUT_LOG, `${line}\n`);
  let sent = false;
  pi.on("tool_call", (event) => {
    record(`tool_call ${event.toolName}`);
    if (sent) return;
    sent = true;
    setImmediate(() => pi.sendUserMessage("What is 20+22?", { deliverAs: "followUp" }));
  });
  pi.on("input", (event) => {
    if (event.source !== "extension") return;
    record(`input ${event.text}`);
    setImmediate(() => record("input-immediate"));
  });
  pi.on("tool_result", (event) => record(`tool_result ${event.toolName}`));
  pi.on("turn_end", () => record("turn_end"));
}
