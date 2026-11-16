// The refused send of rpc/84 made first in one synchronous run of a turn_end handler instead of a tool_call handler: an event handler the loop awaits
// is the same sending code, so Pi's catch callback (agent-session.ts:3552-3555 at b4afdddf44e0) still runs after the handler's run, after the
// queue_update of the steer and of the follow-up sent behind the refused message.
export default function (pi) {
  let sent = false;
  pi.on("turn_end", () => {
    if (sent) return;
    sent = true;
    pi.sendUserMessage("delivery-absent");
    pi.sendUserMessage("delivery-steer", { deliverAs: "steer" });
    pi.sendUserMessage("delivery-followUp", { deliverAs: "followUp" });
  });
}
