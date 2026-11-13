// An extension whose tool_call handler aborts the run while it decides about the first tool call of a batch, the way a user presses
// Esc at a permission prompt. Used by TestRPCAbortedBatchAnswersEveryCallInOrder and the parity scenario
// rpc/abort-mid-batch-answers-every-call (Pi 4438c58ba, #9986).
export default function abortFirstToolCall(pi) {
  let asked = 0;
  pi.on("tool_call", (_event, ctx) => {
    asked++;
    if (asked === 1) ctx.abort();
  });
}
