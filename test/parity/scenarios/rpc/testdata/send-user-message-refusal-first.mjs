// An extension sends three user messages in ONE synchronous run of a tool_call handler, while the agent streams, the refused one FIRST: one with no
// delivery, a steer and a followUp. Pi calls the async session method and hangs `.catch(runner.emitError)` on its Promise (agent-session.ts
// :3552-3555 at b4afdddf44e0); the refusal rejects that Promise (:2240-2244), so the extension error is emitted in a microtask, after the handler's
// synchronous run: after the queue_update of the steer and of the follow-up, which are emitted while the calls execute. rpc/83 sends the refused
// message last, where both orders agree.
export default function (pi) {
  let sent = false;
  pi.on("tool_call", () => {
    if (sent) return;
    sent = true;
    pi.sendUserMessage("delivery-absent");
    pi.sendUserMessage("delivery-steer", { deliverAs: "steer" });
    pi.sendUserMessage("delivery-followUp", { deliverAs: "followUp" });
  });
}
