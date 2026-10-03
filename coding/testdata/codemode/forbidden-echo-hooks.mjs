// The extension factory of "routes nested calls through extension hooks"
// (agent-session-codemode.test.ts:186-203): block echo of "forbidden" and redact the stats result.
export default function (pi) {
  pi.on("tool_call", (event) => {
    if (event.toolName === "echo" && event.input.text === "forbidden") {
      return { block: true, reason: "echo of forbidden text is blocked" };
    }
    return undefined;
  });
  pi.on("tool_result", (event) => {
    if (event.toolName === "stats") {
      return { content: [{ type: "text", text: "redacted" }] };
    }
    return undefined;
  });
}
