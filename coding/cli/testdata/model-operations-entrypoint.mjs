import { writeFileSync } from "node:fs";

export default function (pi) {
  pi.on("session_start", async (_event, ctx) => {
    const model = ctx.modelRegistry.find("test-faux", "faux-1");
    if (!ctx.model || ctx.model.id !== "faux-1" || !model) throw new Error("model lookup unavailable");
    const auth = await ctx.modelRegistry.getApiKeyAndHeaders(model);
    if (!auth.ok) throw new Error(`auth unavailable: ${JSON.stringify(auth)}`);
    const request = { messages: [{ role: "user", content: "What is 20+22?" }] };
    const stream = ctx.modelRegistry.stream(model, request);
    const result = await stream.result();
    if (result.stopReason !== "stop") throw new Error(`stream failed: ${JSON.stringify(result)}`);
    const completed = await ctx.modelRegistry.complete(model, request);
    if (completed.stopReason !== "stop") throw new Error(`complete failed: ${JSON.stringify(completed)}`);
    writeFileSync(process.env.PIG_MODEL_OPERATIONS_MARKER, String(ctx.mode));
  });
}
