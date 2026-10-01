// parity-result-details: a tool_result handler that only adds details to a bash result.
// The runner keeps the chained content, isError and structuredContent (runner.ts:1187,1228-1234), so afterToolCall returns a result
// and agent-loop.ts:877-889 spreads it over a new object: every key keeps its position, so structuredContent stays before isError.
export default function (pi) {
  pi.on("tool_result", (event) => {
    if (event.toolName === "bash") return { details: { note: "kept" } };
  });
}
