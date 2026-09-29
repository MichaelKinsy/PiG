// A real Node extension whose message_start handler awaits a timer, like a Pi handler that awaits I/O (runner.ts:emit awaits each handler; agent-session.ts:894-919 awaits _emitExtensionEvent).
export default function (pi) {
  pi.on("message_start", async (event) => {
    if (event.message.role !== "assistant") return;
    await new Promise((resolve) => setTimeout(resolve, 150));
  });
}
