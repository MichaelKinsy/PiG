import http from "node:http";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { createAgentSession, DefaultResourceLoader, SessionManager, SettingsManager } from "@earendil-works/pi-coding-agent";

// A nested Session that shares the parent's model runtime: the extension hands Pi's createAgentSession the ModelRuntime that ctx.modelRegistry wraps (model-registry.ts keeps it in its `runtime` field), as a completion auditor does to reuse the parent's providers and credentials.
export default async function (pi) {
  const requests = [];
  const server = http.createServer(async (req, res) => {
    let bytes = "";
    for await (const chunk of req) bytes += chunk;
    requests.push({ authorization: req.headers.authorization, model: JSON.parse(bytes).model });
    res.writeHead(200, { "content-type": "text/event-stream" });
    res.end(`data: ${JSON.stringify({ id: "shared", choices: [{ delta: { content: "shared answer" }, finish_reason: "stop" }], usage: { prompt_tokens: 3, completion_tokens: 2, total_tokens: 5 } })}\n\ndata: [DONE]\n\n`);
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  pi.registerProvider("shared-sdk", {
    api: "openai-completions",
    apiKey: "shared-key",
    baseUrl: `http://127.0.0.1:${server.address().port}/v1`,
    models: [{ id: "shared-model", name: "shared-model", reasoning: false, input: ["text"], contextWindow: 32768, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }],
  });
  pi.on("session_shutdown", async () => {
    server.closeAllConnections();
    await new Promise((resolve) => server.close(resolve));
  });
  pi.registerCommand("child-shared", {
    description: "Run a nested Session on the parent's model runtime",
    async handler(destination, ctx) {
      mkdirSync(dirname(destination), { recursive: true });
      const runtime = ctx.modelRegistry.runtime;
      const model = ctx.modelRegistry.find("shared-sdk", "shared-model");
      const settingsManager = SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false }, cacheWarming: { mode: "off" } });
      const loader = new DefaultResourceLoader({ cwd: dirname(destination), agentDir: dirname(destination), settingsManager, noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true, systemPrompt: "Shared runtime child" });
      await loader.reload();
      const { session } = await createAgentSession({ cwd: dirname(destination), agentDir: dirname(destination), modelRuntime: runtime, model, sessionManager: SessionManager.inMemory(dirname(destination)), settingsManager, resourceLoader: loader, tools: [] });
      try {
        await session.bindExtensions({ mode: "print", onError: (error) => { throw new Error(error.error); } });
        await session.prompt("ask the shared provider");
        const last = session.messages.at(-1);
        const output = {
          answer: last.content?.[0]?.text,
          stopReason: last.stopReason,
          errorMessage: last.errorMessage,
          sharedRuntime: session.modelRuntime === runtime,
          configured: runtime.hasConfiguredAuth("shared-sdk"),
          unconfigured: runtime.hasConfiguredAuth("no-such-provider"),
          oauth: runtime.isUsingOAuth("shared-sdk"),
          requests,
        };
        writeFileSync(destination, JSON.stringify(output, null, 2) + "\n");
      } finally {
        session.dispose();
      }
      ctx.ui.notify("shared-artifact-written", "info");
    },
  });
}
