import http from "node:http";

export default async function (pi) {
  const observations = [];
  let mode = "response";
  const server = http.createServer((_request, response) => {
    response.writeHead(200, { "content-type": "text/event-stream", "x-probe": ["first", "second"] });
    response.end('data: {"id":"probe","choices":[{"delta":{"content":"done"},"finish_reason":"stop"}]}\n\ndata: [DONE]\n\n');
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  pi.registerProvider("event-prompt-probe", {
    api: "openai-completions", baseUrl: `http://127.0.0.1:${server.address().port}/v1`, apiKey: "probe-key",
    models: [{ id: "probe", name: "probe", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 128000, maxTokens: 1000 }],
  });
  const tool = (name, promptSnippet, promptGuidelines) => pi.registerTool({
    name, label: name, description: "This description must not become a snippet", promptSnippet, promptGuidelines,
    parameters: { type: "object", properties: {} }, execute: async () => ({ content: [{ type: "text", text: "unused" }] }),
  });
  tool("read", undefined, ["  override read  "]);
  tool("zeta", " \ufeffZeta\r\n  summary\t ", [" shared ", "zeta rule", "shared", " "]);
  tool("alpha", "Alpha summary", ["alpha rule", " shared "]);
  tool("hidden", " \n\t ", []);
  pi.on("session_start", () => pi.setActiveTools(["zeta", "read", "alpha", "hidden", "zeta", "unknown"]));
  pi.on("before_agent_start", (event) => {
    mode = event.prompt;
    if (mode === "prompt") {
      for (const name of ["tools", "rules"]) observations.push(event.systemPrompt.match(new RegExp(`<${name}>[\\s\\S]*?</${name}>`))?.[0] ?? `missing ${name}`);
    }
  });
  pi.on("after_provider_response", async (event) => {
    await Promise.resolve();
    if (mode === "response") observations.push(`first:${event.type}:${event.status}:${event.headers["x-probe"]}`);
    return { cancel: true };
  });
  pi.on("after_provider_response", () => {
    if (mode === "response") observations.push("second");
  });
  pi.on("message_end", (event) => {
    if (event.message.role !== "assistant") return;
    if (mode === "response") observations.push("assistant");
    return { message: { ...event.message, content: [{ type: "text", text: observations.join("\n") }] } };
  });
  pi.on("session_shutdown", () => new Promise((resolve) => server.close(resolve)));
}
