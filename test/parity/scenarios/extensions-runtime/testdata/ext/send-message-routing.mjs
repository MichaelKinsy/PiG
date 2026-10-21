// An extension sends three custom messages while a run streams: pi.sendMessage with no options, with deliverAs "followUp", and with triggerTurn false.
// The steered and follow-up messages ask the faux provider's one canned question, so every model call the run makes has an answer (the provider rejects any other text); the label each renders under (the customType) tells them apart.
// Pi's sendCustomMessage (agent-session.ts:2323-2359) steers the first (delivered before the next model call), queues the second until the run would stop, and holds the third until the turn's tool results are complete, then appends it without a model call.
export default function (pi) {
  let sent = false;
  pi.on("tool_result", (event) => {
    if (sent || event.toolName !== "read") return;
    sent = true;
    pi.sendMessage({ customType: "route-default", content: "What is 20+22?", display: true });
    pi.sendMessage({ customType: "route-follow-up", content: "What is 20+22?", display: true }, { deliverAs: "followUp" });
    pi.sendMessage({ customType: "route-quiet", content: "route-quiet-body", display: true }, { triggerTurn: false });
  });
}
