export default function (pi) {
  pi.registerTool({
    name: "timer_order",
    label: "timer_order",
    description: "Queue a notification on each kind of deferred callback.",
    parameters: { type: "object", properties: {} },
    async execute(_toolCallId, _params, _signal, _onUpdate, ctx) {
      const note = (message) => ctx.ui.notify(message, "info");
      process.nextTick(() => note("timer-order:nextTick"));
      setImmediate(() => note("timer-order:immediate"));
      setTimeout(() => note("timer-order:timeout0"), 0);
      queueMicrotask(() => note("timer-order:queueMicrotask"));
      Promise.resolve().then(() => note("timer-order:promise"));
      note("timer-order:sync");
      return { content: [{ type: "text", text: "timer-order-done" }], details: {} };
    },
  });
}
