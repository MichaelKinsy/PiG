export default function (pi) {
  pi.registerCommand("model_registry_runtime", {
    description: "verify model registry streaming and completion",
    handler: async (_args, ctx) => {
      const model = ctx.modelRegistry.find("openrouter", "org/model/name");
      if (!model) throw new Error("model not found");
      const stream = ctx.modelRegistry.stream(model, { messages: [{ role: "user", content: "hello" }] });
      const types = [];
      for await (const event of stream) types.push(event.type);
      const result = await stream.result();
      if (types.join(",") !== "start,text_start,text_delta,text_end,done") {
        throw new Error(`event types = ${types.join(",")}`);
      }
      if (result.content?.[0]?.text !== "streamed") throw new Error(`stream result = ${JSON.stringify(result)}`);
      const completed = await ctx.modelRegistry.complete(model, { messages: [{ role: "user", content: "hello" }] });
      if (completed.content?.[0]?.text !== "streamed") throw new Error(`complete result = ${JSON.stringify(completed)}`);
      const simple = ctx.modelRegistry.streamSimple(model, { messages: [{ role: "user", content: "hello" }] });
      const simpleResult = await simple.result();
      if (simpleResult.content?.[0]?.text !== "streamed") throw new Error(`simple result = ${JSON.stringify(simpleResult)}`);
    },
  });
}
