// A tool that activates another tool with pi.setActiveTools and ends its run (terminate), followed by a run that agent_settled starts with pi.sendMessage and triggerTurn. Pi's setActiveToolsByName rebuilds the base prompt options and the next run's system patch lists the new tool's snippet.
export default function (pi) {
  const tool = (name, snippet, execute) => ({
    name,
    label: name,
    description: `${name} tool`,
    promptSnippet: snippet,
    parameters: { type: "object", properties: {} },
    execute,
  });
  pi.registerTool(tool("late_tool", "Run late_tool when the late work is due.", async () => ({ content: [{ type: "text", text: "late" }] })));
  pi.registerTool(tool("activate_late", "Run activate_late to switch late_tool on.", async () => {
    pi.setActiveTools([...pi.getActiveTools(), "late_tool"]);
    return { content: [{ type: "text", text: "activated" }], terminate: true };
  }));
  pi.on("session_start", () => {
    pi.setActiveTools(pi.getActiveTools().filter((name) => name !== "late_tool"));
  });
  let settled = 0;
  pi.on("agent_settled", (_event, ctx) => {
    settled += 1;
    // The status marks the second run's end for the scenario's barrier.
    if (settled === 2) ctx.ui.setStatus("continued", "done");
    if (settled !== 1) return;
    pi.sendMessage({ customType: "again", content: [{ type: "text", text: "Continue the work." }], display: true }, { triggerTurn: true });
  });
}
