// parity-null-details: an extension tool that returns details: null. A tool_result handler records the details it was given without changing the result, a turn_end handler records the turn's toolResult messages, and session_shutdown records the toolResult messages as the session file stores them.
import { appendFileSync, readFileSync } from "node:fs";

export default function (pi) {
  const report = (row) => appendFileSync(process.env.PARITY_DETAILS_REPORT, JSON.stringify(row) + "\n");
  pi.registerTool({
    name: "echo_bridge",
    description: "Echo back the provided text with null details.",
    parameters: { type: "object", properties: { text: { type: "string" } }, required: ["text"] },
    async execute(_toolCallId, params) {
      return { content: [{ type: "text", text: `echo-bridge: ${params.text}` }], details: null };
    },
  });
  pi.on("tool_result", (event) => {
    report({ event: "tool_result", details: event.details, isNull: event.details === null });
  });
  pi.on("turn_end", (event) => {
    // The turn's toolResult messages as the turn_end event hands them to handlers.
    for (const message of event.toolResults) report({ event: "turn_end", message });
  });
  pi.on("session_shutdown", (_event, ctx) => {
    const file = ctx.sessionManager.getSessionFile();
    for (const line of readFileSync(file, "utf8").split("\n")) {
      if (!line) continue;
      const entry = JSON.parse(line);
      if (entry.type === "message" && entry.message.role === "toolResult") {
        // The stored message as the file holds it: JSON.parse keeps a null member and an absent one apart.
        report({ event: "session_file", message: entry.message });
      }
    }
  });
}
