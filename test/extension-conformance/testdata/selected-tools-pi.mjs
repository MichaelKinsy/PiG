import { pathToFileURL } from "node:url";
import { resolve } from "node:path";
const root = process.argv[2];
const { ExtensionRunner } = await import(pathToFileURL(resolve(root, "dist/core/extensions/runner.js")));
const { AgentSession } = await import(pathToFileURL(resolve(root, "dist/core/agent-session.js")));
const { normalizeBuildSystemPromptOptions } = await import(pathToFileURL(resolve(root, "dist/core/system-prompt.js")));

const output = {};
for (const kind of ["duplicates", "mixed", "null", "repair", "reassign", "getters"]) {
  const observed = [];
  const handlers = [async (event, ctx) => {
    const options = event.systemPromptOptions;
    if (kind === "duplicates") options.selectedTools = ["bash", "read", "bash", "missing"];
    if (kind === "mixed") options.selectedTools = [null, 17, "bash", "bash"];
    if (kind === "null" || kind === "repair") options.selectedTools = null;
    if (kind === "reassign") {
      options.selectedTools.push("bash");
      event.systemPromptOptions = { ...options, selectedTools: ["write"] };
    }
    if (kind === "getters") {
      options.sections.live = "updated";
      observed.push(event.systemPrompt.includes("<live>\nupdated\n</live>"));
      observed.push(ctx.getSystemPrompt().includes("<live>\nupdated\n</live>"));
    }
  }, async (event) => {
    if (kind === "repair") {
      observed.push(event.systemPromptOptions.selectedTools === null);
      event.systemPromptOptions.selectedTools = ["write"];
    }
  }];
  const runner = new ExtensionRunner([{ path: "probe", handlers: new Map([["before_agent_start", handlers]]) }], {}, "/probe", {}, {});
  const session = Object.create(AgentSession.prototype);
  const registry = new Map(["read", "bash", "write"].map(name => [name, { name }]));
  Object.assign(session, {
    agent: { state: { model: { provider: "probe" }, messages: [], tools: [registry.get("read")] } },
    _toolRegistry: registry,
    _baseSystemPromptOptions: normalizeBuildSystemPromptOptions({ cwd: "/probe", customPrompt: "base", selectedTools: ["read"] }),
    _extensionRunner: runner,
    _modelRuntime: { hasConfiguredAuth: () => true },
    _resourceLoader: { getPrompts: () => ({ prompts: [] }) },
    _pendingNextTurnMessages: [],
    _runInputHandlers: async (text, images) => ({ text, images }),
    _expandSkillCommand: text => text,
    _flushPendingBashMessages() {},
    _flushPendingCustomMessages() {},
    _findLastAssistantMessage() {},
    _normalizePromptImages: async () => ({ images: [], hints: [] }),
    _runAgentPrompt: async () => { observed.push("run"); },
  });
  let error = null;
  try { await session.prompt("probe"); } catch (err) { error = err.message; }
  output[kind] = { tools: session.getActiveToolNames(), base: session._baseSystemPromptOptions.selectedTools, observed, error };
}
console.log(JSON.stringify(output));
