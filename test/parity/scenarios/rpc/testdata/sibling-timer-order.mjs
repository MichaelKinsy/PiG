export default function (pi) {
  pi.registerTool({
    name: "dialog_pick",
    label: "dialog_pick",
    description: "First awaits a timer; the second queues deferred notifications and returns.",
    parameters: { type: "object", properties: { title: { type: "string" } }, required: ["title"] },
    async execute(_toolCallId, params, _signal, _onUpdate, ctx) {
      const note = (message) => ctx.ui.notify(message, "info");
      if (params.title === "First?") {
        await new Promise((resolve) => setTimeout(resolve, 300));
        note("sibling:first-done");
      } else {
        process.nextTick(() => note("sibling:nextTick"));
        setImmediate(() => note("sibling:immediate"));
        setTimeout(() => note("sibling:timeout0"), 0);
        note("sibling:sync");
      }
      return { content: [{ type: "text", text: params.title }], details: {} };
    },
  });
}
