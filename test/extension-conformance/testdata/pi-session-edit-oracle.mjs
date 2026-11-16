// The body of the statements TestInPlaceMessageEventEditsMatchPi runs under Node (internal/testenv.NodeJSON): Pi's own AgentSession (the genuine dist) over a local OpenAI-completions server, with three handlers of one event: the first edits the event (input.level), the second, of the same extension, observes it, and a second extension observes it too. It sends two prompts and reports what each handler saw, what the session entries hold, whether each request body carries the edit, and which events the session's own listeners received with the edit. The edit writes the value `other-event-edit`.
const http = await import("node:http");
const { mkdtempSync } = await import("node:fs");
const { join } = await import("node:path");
const { tmpdir } = await import("node:os");
const { createAgentSession, DefaultResourceLoader, ModelRuntime, SessionManager, SettingsManager } = await import(input.pi);
const X = "other-event-edit";
const runRow = async ({ event, level }) => {
  const dir = mkdtempSync(join(tmpdir(), "oracle-"));
  const requests = [];
  const server = http.createServer(async (req, res) => {
    let bytes = ""; for await (const chunk of req) bytes += chunk;
    requests.push(bytes);
    res.writeHead(200, { "content-type": "text/event-stream" });
    res.end(`data: ${JSON.stringify({ id: "t", choices: [{ delta: { content: "reply " + requests.length }, finish_reason: "stop" }], usage: { prompt_tokens: 1, completion_tokens: 1, total_tokens: 2 } })}\n\ndata: [DONE]\n\n`);
  });
  await new Promise(r => server.listen(0, "127.0.0.1", r));
  const runtime = await ModelRuntime.create({ authPath: join(dir, "auth.json"), modelsPath: join(dir, "models.json"), allowModelNetwork: false });
  runtime.registerProvider("probe", { api: "openai-completions", apiKey: "k", baseUrl: `http://127.0.0.1:${server.address().port}/v1`, models: [{ id: "probe", name: "probe", reasoning: false, input: ["text"], contextWindow: 32768, maxTokens: 1024, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 } }] });
  await runtime.refresh({ allowNetwork: false });
  const settings = SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false }, cacheWarming: { mode: "off" } });
  const target = e => e.message ?? (Array.isArray(e.messages) ? (e.messages.find(m => m.role === "user") ?? e.messages[0]) : undefined);
  const observed = [];
  const watch = who => e => observed.push({ who, role: target(e)?.role ?? "", top: e.probeTop === X, deep: JSON.stringify(e.message ?? e.messages ?? null).includes(X) || target(e)?.probe === X });
  const editor = e => {
    if (level === "top") { e.probeTop = X; return; }
    const t = target(e);
    if (!t) return;
    if (Array.isArray(t.content) && t.content[0] && typeof t.content[0] === "object" && t.content[0].type === "text") t.content[0].text = X;
    else if (typeof t.content === "string") t.content = X;
    else t.probe = X;
  };
  const on = (event, fn) => ext => ext.on(event, fn);
  const loader = new DefaultResourceLoader({ cwd: dir, agentDir: dir, settingsManager: settings, noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true, systemPrompt: "s",
    extensionFactories: [ext => { ext.on(event, editor); ext.on(event, watch("A")); }, ext => { ext.on(event, watch("B")); }] });
  await loader.reload();
  const manager = SessionManager.inMemory(dir);
  const { session } = await createAgentSession({ cwd: dir, agentDir: dir, modelRuntime: runtime, model: runtime.getModel("probe", "probe"), sessionManager: manager, settingsManager: settings, resourceLoader: loader, tools: [] });
  const events = [];
  session.subscribe(e => events.push({ type: e.type, role: target(e)?.role ?? "", hit: JSON.stringify(e).includes(X) }));
  await session.bindExtensions({ mode: "print", onError: error => { throw new Error(error.error); } });
  await session.prompt("first");
  await session.prompt("second");
  const entries = manager.getEntries().filter(e => e.type === "message").map(e => ({ role: e.message.role, hit: JSON.stringify(e.message).includes(X) }));
  server.close();
  return { observed, entries, requests: requests.map(r => r.includes(X)), events };
};
const out = {};
for (const row of input.rows) out[row.event + "/" + row.level] = await runRow(row);
return out;
