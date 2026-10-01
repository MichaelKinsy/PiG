// The extension factory of "keeps structured content that tool_result handlers replace along with the content"
// (agent-session-codemode.test.ts:268-279).
export default function (pi) {
  pi.on("tool_result", (event) =>
    event.toolName === "stats"
      ? { content: [{ type: "text", text: "0 files" }], structuredContent: { files: 0, names: [] } }
      : undefined,
  );
  // A later handler that only touches details keeps what the first one set.
  pi.on("tool_result", (event) => (event.toolName === "stats" ? { details: { audited: true } } : undefined));
}
