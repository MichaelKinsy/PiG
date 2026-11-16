// An extension sends three user messages from a tool_call handler, while the agent streams: one with no delivery, one steer and one followUp.
// Pi passes `options?.deliverAs` to prompt() as streamingBehavior, which refuses a streaming prompt without one (agent-session.ts:2240-2244) and
// reports the refusal as the extension error `send_user_message` (:3565-3572); the steer joins the run and the follow-up follows it.
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
