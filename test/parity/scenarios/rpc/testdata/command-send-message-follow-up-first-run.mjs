// An extension command starts the session's first run with pi.sendMessage, triggerTurn and deliverAs "followUp" while the agent is idle, as a search extension reports its result. Pi's sendCustomMessage starts the run with _startRun for a custom message whatever deliverAs says, so the request carries the system prompt with the extension's tool snippet.
export default function (pi) {
  pi.registerTool({
    name: "snippet_tool",
    label: "snippet_tool",
    description: "snippet tool",
    promptSnippet: "Use snippet_tool for the snippet work.",
    parameters: { type: "object", properties: {} },
    execute: async () => ({ content: [{ type: "text", text: "done" }] }),
  });
  pi.registerCommand("report", {
    handler: () => {
      pi.sendMessage({ customType: "report", content: [{ type: "text", text: "The report is ready." }], display: true }, { triggerTurn: true, deliverAs: "followUp" });
    },
  });
}
