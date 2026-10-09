export default function (pi) {
  pi.registerCommand("model_operations", {
    description: "verify shared model operations",
    handler: async (_args, ctx) => {
      if (!ctx.model || ctx.model.id !== "faux-1") throw new Error(`current model = ${JSON.stringify(ctx.model)}`);
      const model = ctx.modelRegistry.find("test-faux", "faux-1");
      if (!model) throw new Error("model not found");
      const auth = await ctx.modelRegistry.getApiKeyAndHeaders(model);
      if (!auth.ok) throw new Error(`auth = ${JSON.stringify(auth)}`);
      const request = { messages: [{ role: "user", content: "What is 20+22?" }] };
      const stream = ctx.modelRegistry.stream(model, request);
      const types = [];
      for await (const event of stream) types.push(event.type);
      const result = await stream.result();
      if (result.stopReason !== "stop" || !types.includes("done")) throw new Error(`stream = ${types}: ${JSON.stringify(result)}`);
      const completed = await ctx.modelRegistry.complete(model, request);
      if (completed.stopReason !== "stop") throw new Error(`complete = ${JSON.stringify(completed)}`);
    },
  });
}
