// parity-blocker: registers a tool_call handler that blocks any bash tool
// call containing "BLOCK_ME" with reason "parity-blocked".
// Exercises runner.ts tool_call event dispatch + block semantics.
export default function (pi) {
  pi.on("tool_call", (event, ctx) => {
    if (event.toolName === "bash" && event.input && event.input.command && event.input.command.includes("BLOCK_ME")) {
      return { block: true, reason: "parity-blocked" };
    }
    return undefined;
  });
}
