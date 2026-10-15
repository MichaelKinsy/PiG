// Records the order of a print-mode prompt's last extension call and session_shutdown.
// Pi's print and JSON modes dispose the runtime in the continuation of the prompt
// (print-mode.ts:131-166, agent-session-runtime.ts:404-410), so when session_shutdown
// arrives, an immediate queued by the command, the handled input or agent_settled has not
// run. agent_settled schedules a follow-up turn the way pi-goal-x schedules its next goal
// turn, and session_shutdown cancels it, as pi-goal-x does.
import { appendFileSync } from "node:fs";

export default function (pi) {
  const record = (line) => appendFileSync(process.env.PROMPT_END_LOG, `${line}\n`);
  const pending = new Map();
  const queue = (name, run) => pending.set(name, setImmediate(() => {
    pending.delete(name);
    run();
  }));
  pi.registerCommand("prompt-end", {
    description: "Ends the prompt",
    handler: async () => {
      record("command");
      queue("command", () => record("command-immediate"));
    },
  });
  pi.on("input", (event) => {
    if (event.text !== "handled input") return;
    record("input");
    queue("input", () => record("input-immediate"));
    return { action: "handled" };
  });
  pi.on("agent_settled", () => {
    record("agent_settled");
    queue("agent_settled", () => pi.sendMessage(
      { customType: "prompt-end", content: "continue", display: false },
      { triggerTurn: true, deliverAs: "followUp" },
    ));
  });
  pi.on("session_shutdown", () => {
    record(`session_shutdown pending=${[...pending.keys()].join(",")}`);
    for (const immediate of pending.values()) clearImmediate(immediate);
    pending.clear();
  });
}
