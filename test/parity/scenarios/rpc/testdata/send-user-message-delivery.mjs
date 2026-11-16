// An extension sends three user messages from a tool_call handler, while the agent streams: a steer, a followUp and one with no delivery.
// Pi passes `options?.deliverAs` to prompt() as streamingBehavior, which refuses a streaming prompt without one (agent-session.ts:2240-2244) and
// reports the refusal as the extension error `send_user_message` (:3565-3572); the steer joins the run and the follow-up follows it.
// The refused message is sent last: Pi reports the refusal from the rejected Promise's handler, a microtask after the handler's synchronous run, so
// a refusal sent first is reported by Pi after the later sends' queue updates; the host reports it when it receives the call, which is a position
// the recorded stream cannot give a Node handler (the host has no view of the extension's synchronous run). Sent last, both place it after them.
export default function (pi) {
  let sent = false;
  pi.on("tool_call", () => {
    if (sent) return;
    sent = true;
    pi.sendUserMessage("delivery-steer", { deliverAs: "steer" });
    pi.sendUserMessage("delivery-followUp", { deliverAs: "followUp" });
    pi.sendUserMessage("delivery-absent");
  });
}
