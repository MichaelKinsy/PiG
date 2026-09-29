import assert from "node:assert/strict";
import http from "node:http";
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { createAgentSession, DefaultResourceLoader, ModelRuntime, SessionManager, SettingsManager } from "@earendil-works/pi-coding-agent";

export default function (pi) {
  pi.registerCommand("child-sdk", {
    description: "Exercise an independent SDK Session over a real provider",
    async handler(destination, ctx) {
      const parent = { model: ctx.model, entries: ctx.sessionManager.getEntries(), tools: pi.getActiveTools() };
      const dir = dirname(destination);
      mkdirSync(dir, { recursive: true });
      const requests = [];
      const hooks = [];
      let abortReceived;
      const abortReady = new Promise(resolve => { abortReceived = resolve; });
      const server = http.createServer(async (req, res) => {
        let bytes = "";
        for await (const chunk of req) bytes += chunk;
        const body = JSON.parse(bytes);
        requests.push({ model: body.model, messages: body.messages, tools: body.tools, header: req.headers["x-child"], marker: body.child_marker });
        if (body.model === "abort-child") {
          res.writeHead(200, { "content-type": "text/event-stream" });
          res.flushHeaders();
          abortReceived();
          return;
        }
        const step = requests.filter(r => r.model === body.model).length;
        let delta;
        if (step === 1) delta = { tool_calls: [{ index: 0, id: "write-child", type: "function", function: { name: "write", arguments: JSON.stringify({ path: "child.txt", content: "independent tool contents" }) } }] };
        else if (step === 2) delta = { tool_calls: [{ index: 0, id: "read-child", type: "function", function: { name: "read", arguments: JSON.stringify({ path: "child.txt" }) } }, { index: 1, id: "audit-child", type: "function", function: { name: "audit", arguments: "{}" } }] };
        else delta = { content: "independent answer" };
        res.writeHead(200, { "content-type": "text/event-stream", "x-child-response": "observed" });
        res.end(`data: ${JSON.stringify({ id: "child", choices: [{ delta, finish_reason: step < 3 ? "tool_calls" : "stop" }], usage: { prompt_tokens: 11, completion_tokens: 7, total_tokens: 18 } })}\n\ndata: [DONE]\n\n`);
      });
      await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
      const modelRuntime = await ModelRuntime.create({ authPath: join(dir, "auth.json"), modelsPath: join(dir, "models.json"), allowModelNetwork: false });
      modelRuntime.registerProvider("independent-sdk", {
        api: "openai-completions", apiKey: "child-key", baseUrl: `http://127.0.0.1:${server.address().port}/v1`,
        models: ["tool-child", "abort-child"].map(id => ({ id, name: id, reasoning: false, input: ["text"], contextWindow: 32768, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } })),
      });
      await modelRuntime.refresh({ allowNetwork: false });
      const settingsManager = SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false }, cacheWarming: { mode: "off" } });
      const loader = new DefaultResourceLoader({
        cwd: dir, agentDir: dir, settingsManager, noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true,
        systemPrompt: "Independent system instructions", appendSystemPrompt: ["Independent appendix"],
        extensionFactories: [extension => {
          extension.on("before_provider_headers", event => { event.headers["x-child"] = "private"; hooks.push("headers"); });
          extension.on("before_provider_request", payload => { hooks.push("payload"); return { ...payload.payload, child_marker: "private" }; });
          extension.on("after_provider_response", async event => { await Promise.resolve(); assert.equal(event.headers["x-child-response"], "observed"); hooks.push("response"); });
        }],
      });
      await loader.reload();
      const manager = SessionManager.create(dir, join(dir, "sessions"));
      const { session } = await createAgentSession({
        cwd: dir, agentDir: dir, modelRuntime, model: modelRuntime.getModel("independent-sdk", "tool-child"),
        sessionManager: manager, settingsManager, resourceLoader: loader, tools: ["write", "read", "audit"],
        customTools: [{ name: "audit", label: "audit", description: "Independent custom tool", parameters: { type: "object", properties: {} }, async execute(_id, _args, _signal, update) {
          update?.({ content: [{ type: "text", text: "audit progress" }] });
          await Promise.resolve();
          return { content: [{ type: "text", text: "audit done" }] };
        } }],
      });
      const events = [];
      const unsubscribe = session.subscribe(event => events.push(event.type === "tool_execution_end" ? `${event.type}:${event.toolName}:${event.isError}` : event.type));
      let abortSession;
      try {
        await session.bindExtensions({ mode: "print", onError: error => { throw new Error(error.error); } });
        await session.prompt("run independent tools");
        assert.equal(session.messages.at(-1).content[0].text, "independent answer");
        assert.equal(readFileSync(join(dir, "child.txt"), "utf8"), "independent tool contents");
        assert.equal(session.isStreaming, false);
        assert.deepEqual(hooks, ["headers", "payload", "response", "headers", "payload", "response", "headers", "payload", "response"]);
        for (const request of requests) {
          assert.equal(request.header, "private");
          assert.equal(request.marker, "private");
          assert.ok(JSON.stringify(request.messages).includes("Independent appendix"));
          assert.deepEqual(request.tools.map(t => t.function.name), ["write", "read", "audit"]);
        }
        const reopened = SessionManager.open(manager.getSessionFile());
        assert.equal(reopened.buildSessionContext().messages.at(-1).content[0].text, "independent answer");
        const created = await createAgentSession({ cwd: dir, agentDir: dir, modelRuntime, model: modelRuntime.getModel("independent-sdk", "abort-child"), sessionManager: SessionManager.inMemory(dir), settingsManager, tools: [], resourceLoader: { ...loader, getExtensions: () => ({ extensions: [], errors: [], runtime: loader.getExtensions().runtime }), getSkills: () => ({ skills: [], diagnostics: [] }), getPrompts: () => ({ prompts: [], diagnostics: [] }), getThemes: () => ({ themes: [], diagnostics: [] }), getAgentsFiles: () => ({ agentsFiles: [] }), getSystemPrompt: () => "cancel child", getAppendSystemPrompt: () => [] } });
        abortSession = created.session;
        const pending = abortSession.prompt("cancel independent request");
        await abortReady;
        await abortSession.abort();
        await pending;
        assert.equal(abortSession.messages.at(-1).stopReason, "aborted", JSON.stringify(abortSession.messages.at(-1)));
        assert.equal(abortSession.isStreaming, false);
        assert.deepEqual(ctx.model, parent.model);
        assert.deepEqual(ctx.sessionManager.getEntries(), parent.entries);
        assert.deepEqual(pi.getActiveTools(), parent.tools);
        const output = { answer: session.messages.at(-1).content[0].text, roles: session.messages.map(m => m.role), hooks, tools: session.getActiveToolNames(), toolResults: session.messages.filter(m => m.role === "toolResult").map(m => ({ name: m.toolName, error: m.isError })), persisted: reopened.buildSessionContext().messages.length === session.messages.length, abort: abortSession.messages.at(-1).stopReason, parentUnchanged: true, progress: events.includes("tool_execution_update") };
        writeFileSync(destination, JSON.stringify(output, null, 2) + "\n");
      } finally {
        unsubscribe();
        abortSession?.dispose();
        session.dispose();
        server.closeAllConnections();
        await new Promise(resolve => server.close(resolve));
      }
    },
  });
}
