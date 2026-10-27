// The Node fixture of the extension_runner row (extension_runner_test.go): Pi's own createExtensionRuntime and ExtensionRunner over two probe extensions a DefaultResourceLoader loaded. Its report is the expectation; testfixture/sessiongraph (Go), testdata/session-graph-rust and the inline Python fixture run the same script through their SDK's classes.
import { DefaultResourceLoader, ExtensionRunner, ModelRegistry, ModelRuntime, SessionManager, SettingsManager, createExtensionRuntime } from "@earendil-works/pi-coding-agent";

const orNull = (value) => (value === undefined ? null : value);
const error = async (fn) => {
  try {
    await fn();
  } catch (e) {
    return "error: " + e.message;
  }
  return "no error";
};
const byName = (map) => Object.fromEntries([...map].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)));
const toolView = (tool) => ({ name: tool.definition.name, description: tool.definition.description, scope: tool.sourceInfo.scope, origin: tool.sourceInfo.origin, path: tool.sourceInfo.path });
const commandView = (command) => ({ name: command.name, invocationName: command.invocationName, description: command.description ?? null, path: command.sourceInfo.path });

export async function extensionRunnerScript({ cwd, agentDir, runnerProbeA, runnerProbeB, mrAuth, mrModels, mrStore }) {
  const r = {};

  // createExtensionRuntime: the data members of a fresh runtime, a flag value and a stale runtime.
  const fresh = createExtensionRuntime();
  const runtimeView = (runtime) => ({
    flagValues: byName(runtime.flagValues), providers: runtime.pendingProviderRegistrations.length, nativeProviders: runtime.pendingNativeProviderRegistrations.length,
    virtualModels: runtime.pendingVirtualModelRegistrations.length, mcpServers: runtime.mcpServers.list().length,
  });
  r.runtime = { initial: runtimeView(fresh), active: await error(() => fresh.assertActive()) };
  fresh.flagValues.set("x", true);
  fresh.flagValues.set("a", "b");
  r.runtime.afterSet = runtimeView(fresh);
  fresh.invalidate("runtime is stale");
  fresh.invalidate("a second message is ignored");
  r.runtime.stale = await error(() => fresh.assertActive());

  // The extensions the runner dispatches to: two probes loaded by a loader.
  const rt = await ModelRuntime.create({ authPath: mrAuth, modelsPath: mrModels, modelsStorePath: mrStore });
  const registry = new ModelRegistry(rt);
  const settings = SettingsManager.inMemory({ compaction: { enabled: false }, retry: { enabled: false }, defaultProvider: "mr-local", defaultModel: "m1" });
  const loader = new DefaultResourceLoader({
    cwd, agentDir, settingsManager: settings, noExtensions: true, noSkills: true, noPromptTemplates: true, noThemes: true, noContextFiles: true,
    additionalExtensionPaths: [runnerProbeA, runnerProbeB],
  });
  await loader.reload();
  const loaded = loader.getExtensions();
  r.loaded = {
    paths: loaded.extensions.map((ext) => ext.path), tools: loaded.extensions.map((ext) => [...ext.tools.keys()]), errors: loaded.errors, sources: loaded.extensions.map((ext) => ext.sourceInfo.source), runtime: runtimeView(loaded.runtime),
  };
  const manager = SessionManager.inMemory(cwd);
  const runner = new ExtensionRunner(loaded.extensions, loaded.runtime, cwd, manager, registry);

  // The registries, before anything is bound.
  const keybindings = { "app.interrupt": "ctrl+c", "app.other": ["ctrl+k"], "app.exit": ["ctrl+d"] };
  const noop = runner.getUIContext();
  r.reads = {
    extensionPaths: runner.getExtensionPaths(),
    tools: runner.getAllRegisteredTools().map(toolView),
    toolDefinition: orNull(runner.getToolDefinition("shared")?.description),
    missingTool: orNull(runner.getToolDefinition("nope")),
    flags: byName(runner.getFlags()),
    flagValues: byName(runner.getFlagValues()),
    shortcuts: [...runner.getShortcuts(keybindings)].map(([key, shortcut]) => ({ key, description: shortcut.description, extensionPath: shortcut.extensionPath })),
    shortcutDiagnostics: runner.getShortcutDiagnostics(),
    commands: runner.getRegisteredCommands().map(commandView),
    commandDiagnostics: runner.getCommandDiagnostics(),
    commandByName: { hello: orNull(runner.getCommand("hello")?.invocationName), twin: orNull(runner.getCommand("twin")?.invocationName), "twin:2": orNull(runner.getCommand("twin:2")?.description), nope: orNull(runner.getCommand("nope")) },
    hasHandlers: { tool_call: runner.hasHandlers("tool_call"), session_shutdown: runner.hasHandlers("session_shutdown") },
    renderers: {
      message: typeof runner.getMessageRenderer("probe-message"), missingMessage: typeof runner.getMessageRenderer("nope"), entry: typeof runner.getEntryRenderer("probe-entry"),
      missingEntry: typeof runner.getEntryRenderer("nope"), markdownTransformers: runner.getMarkdownTransformers().length,
    },
    toolRenderers: {
      alpha: (() => { const found = runner.resolveToolRenderers("alpha", () => undefined); return found ? { renderShell: orNull(found.renderShell), renderCall: typeof found.renderCall, renderResult: typeof found.renderResult } : null; })(),
      other: (() => { const found = runner.resolveToolRenderers("other", () => ({ renderResult: () => undefined })); return found ? { renderShell: orNull(found.renderShell), renderCall: typeof found.renderCall, renderResult: typeof found.renderResult } : null; })(),
      none: orNull(runner.resolveToolRenderers("other", () => undefined)),
    },
    hasUI: runner.hasUI(),
    noUI: { select: orNull(await noop.select("t", ["a"])), confirm: await noop.confirm("t", "m"), input: orNull(await noop.input("t")), editorText: noop.getEditorText() },
    registryIsSame: runner.getModelRegistry() === registry,
    activeTools: await error(() => runner.getActiveTools()),
    mode: runner.createContext().mode,
    cwd: runner.createContext().cwd,
    sessionManagerIsSame: runner.createContext().sessionManager === manager,
    modelRegistryIsSame: runner.createContext().modelRegistry === registry,
    commandContextBeforeBind: { waitForIdle: await runner.createCommandContext().waitForIdle(), newSession: await runner.createCommandContext().newSession(), reload: await runner.createCommandContext().reload() },
    toolContextBeforeBind: (await runner.createToolContext("call-0", undefined).executeTool("alpha", {})).result.content[0].text,
  };
  r.reads.flagValuesAfterSet = (runner.setFlagValue("probe-flag", "changed"), byName(runner.getFlagValues()));
  runner.setFlagValue("probe-bool", true);
  r.reads.flagValuesAfterBool = byName(runner.getFlagValues());

  // Bound actions: every call an extension makes reaches one of these, and is logged.
  const log = [];
  const record = (name) => (...args) => {
    log.push([name, ...args.map((arg) => (arg === undefined ? null : arg))]);
  };
  const mrModel = (await rt.getModels()).find((candidate) => candidate.provider === "mr-local" && candidate.id === "m1");
  const outcome = (callerId, name, args) => ({ toolCall: { type: "toolCall", id: callerId + "/1", name, arguments: args }, result: { content: [{ type: "text", text: "ran " + name }], details: {} }, isError: false });
  const actions = {
    sendMessage: record("sendMessage"), sendUserMessage: record("sendUserMessage"), appendEntry: record("appendEntry"), setSessionName: record("setSessionName"),
    getSessionName: () => "session-name", setLabel: record("setLabel"), getActiveTools: () => ["alpha", "beta"],
    getAllTools: () => [{ name: "alpha", description: "Alpha tool", parameters: { type: "object" }, sourceInfo: { path: "p", source: "local", scope: "temporary", origin: "top-level" } }],
    getSettings: () => ({ theme: "dark" }), setActiveTools: record("setActiveTools"), refreshTools: record("refreshTools"),
    getCommands: () => [{ name: "hello", description: "d", source: "extension", sourceInfo: { path: "p", source: "local", scope: "temporary", origin: "top-level" } }],
    setModel: async (candidate) => (record("setModel")(candidate.id), true), getThinkingLevel: () => "medium", setThinkingLevel: record("setThinkingLevel"),
  };
  const contextActions = {
    getModel: () => mrModel, getScopedModels: () => [{ model: mrModel, thinkingLevel: "low" }], isIdle: () => false, isProjectTrusted: () => true, getSignal: () => undefined,
    abort: record("abort"), hasPendingMessages: () => true, shutdown: record("shutdown"), getContextUsage: () => ({ tokens: 10, contextWindow: 100, percent: 10 }),
    compact: (options) => record("compact")(options?.customInstructions), getSystemPrompt: () => "bound system prompt", getSystemPromptOptions: () => ({ cwd, customPrompt: "custom" }),
    executeTool: async (callerId, name, args) => outcome(callerId, name, args), getCallableTools: () => [{ name: "alpha", label: "Alpha", description: "Alpha tool", parameters: { type: "object" } }],
  };
  runner.bindCore(actions, contextActions);
  r.bound = { activeTools: runner.getActiveTools(), errors: [] };
  const errors = [];
  const off = runner.onError((report) => errors.push({ extensionPath: report.extensionPath, event: report.event, error: report.error }));
  runner.reportUnhandledMcpServers();

  // The emit family, each through two handlers that chain.
  const emits = {};
  await runner.emit({ type: "session_start", reason: "startup" });
  emits.sessionBeforeSwitch = await runner.emit({ type: "session_before_switch", reason: "new" });
  await runner.emit({ type: "agent_start" });
  emits.toolCall = await runner.emitToolCall({ type: "tool_call", toolCallId: "t1", toolName: "bash", input: { command: "ls" } });
  emits.toolCallBlocked = await runner.emitToolCall({ type: "tool_call", toolCallId: "t2", toolName: "bash", input: { command: "forbidden" } });
  emits.toolResult = await runner.emitToolResult({ type: "tool_result", toolCallId: "t1", toolName: "bash", input: { command: "ls" }, content: [{ type: "text", text: "out" }], details: undefined, isError: false });
  emits.inputTransform = await runner.emitInput("t:x", undefined, "interactive");
  emits.inputHandled = await runner.emitInput("handled-me", undefined, "interactive");
  emits.inputContinue = await runner.emitInput("plain", undefined, "rpc");
  emits.context = await runner.emitContext([{ role: "user", content: "keep", timestamp: 1 }, { role: "user", content: "drop-me", timestamp: 2 }]);
  const started = await runner.emitBeforeAgentStart({ messageType: "user", prompt: "do it", images: undefined }, { cwd, customPrompt: "base" });
  const forced = started.systemPromptOptions.forceSystemPrompt;
  emits.beforeAgentStart = { messages: started.messages, forced: { startsWithBase: forced.startsWith("base"), endsWithA: forced.endsWith("[A]") }, customPrompt: started.systemPromptOptions.customPrompt };
  emits.messageEnd = await runner.emitMessageEnd({ type: "message_end", message: { role: "user", content: "hi", timestamp: 1 } });
  emits.providerRequest = await runner.emitBeforeProviderRequest({ model: "m" });
  emits.providerHeaders = await runner.emitBeforeProviderHeaders({ "x-del": "d", "x-keep": "k" });
  emits.userBash = await runner.emitUserBash({ type: "user_bash", command: "ls", excludeFromContext: false, cwd });
  emits.resourcesDiscover = await runner.emitResourcesDiscover(cwd, "startup");
  emits.cacheWarming = await runner.emitCacheWarmingDecision({ type: "cache_warming_decision", warmCost: 1, missCost: 2, continuationProbability: 0.5, action: "warm" });
  const built = [];
  emits.boundary = await runner.emitBoundary({ type: "turn_end", turnIndex: 0, message: { role: "user", content: "x", timestamp: 1 }, toolResults: [], messageEntryId: "m1", toolResultEntryIds: [], outcome: "completed" }, (entries) => {
    built.push(entries.length);
    return { contextEntries: [], contextMessages: [], llmMessages: [], pendingMessages: [], canContinue: true };
  });
  emits.boundaryBuilt = built;
  runner.emitError({ extensionPath: "/x/ext.mjs", event: "manual", error: "reported by hand" });
  r.emits = emits;
  r.errors = errors;

  // The contexts, which read the bound functions at call time.
  const ctx = runner.createContext();
  const contexts = {
    mode: ctx.mode, hasUI: ctx.hasUI, cwd: ctx.cwd, model: ctx.model?.id ?? null, scopedModels: ctx.scopedModels.map((scoped) => [scoped.model.id, scoped.thinkingLevel]), thinkingLevel: ctx.thinkingLevel,
    isIdle: ctx.isIdle(), isProjectTrusted: ctx.isProjectTrusted(), signal: orNull(ctx.signal), hasPendingMessages: ctx.hasPendingMessages(), contextUsage: ctx.getContextUsage(), systemPrompt: ctx.getSystemPrompt(),
  };
  ctx.abort();
  ctx.shutdown();
  ctx.compact({ customInstructions: "short" });
  runner.shutdown();
  const command = runner.createCommandContext();
  runner.bindCommandContext({
    waitForIdle: async () => record("waitForIdle")(), newSession: async (options) => (record("newSession")(options?.parentSession), { cancelled: false }),
    fork: async (entryId, options) => (record("fork")(entryId, options?.position), { cancelled: true }), navigateTree: async (target, options) => (record("navigateTree")(target, options?.label), { cancelled: false }),
    switchSession: async (path) => (record("switchSession")(path), { cancelled: false }), reload: async () => record("reload")(),
  });
  contexts.command = {
    systemPromptOptions: command.getSystemPromptOptions(), waitForIdle: orNull(await command.waitForIdle()), newSession: await command.newSession({ parentSession: "parent.jsonl" }), fork: await command.fork("e1", { position: "at" }),
    navigateTree: await command.navigateTree("e2", { label: "l" }), switchSession: await command.switchSession("other.jsonl"), reload: orNull(await command.reload()),
  };
  const tool = runner.createToolContext("call-1", undefined);
  contexts.tool = { tools: tool.tools.map((candidate) => candidate.name), executeTool: await tool.executeTool("alpha", { text: "x" }, {}) };
  r.contexts = contexts;

  // The UI context: the dialogs report ui_prompt_start and ui_prompt_end to the extensions.
  const uiLog = [];
  const ui = {
    select: async (title, options) => (uiLog.push(["select", title, options]), options[1]), confirm: async (title, message) => (uiLog.push(["confirm", title, message]), true),
    input: async (title, placeholder) => (uiLog.push(["input", title, placeholder ?? null]), "typed"), editor: async (title, prefill) => (uiLog.push(["editor", title, prefill ?? null]), (prefill ?? "") + "!"),
    notify: (message, type) => uiLog.push(["notify", message, type ?? null]), setStatus: (key, text) => uiLog.push(["setStatus", key, text ?? null]), setWorkingMessage: (message) => uiLog.push(["setWorkingMessage", message ?? null]),
    setWorkingVisible: (visible) => uiLog.push(["setWorkingVisible", visible]), setHiddenThinkingLabel: (label) => uiLog.push(["setHiddenThinkingLabel", label ?? null]),
    setWidget: (key, content, options) => uiLog.push(["setWidget", key, content, options?.placement ?? null]), setTitle: (title) => uiLog.push(["setTitle", title]),
    pasteToEditor: (text) => uiLog.push(["pasteToEditor", text]), setEditorText: (text) => uiLog.push(["setEditorText", text]), getEditorText: () => "editor text",
    getToolsExpanded: () => true, setToolsExpanded: (expanded) => uiLog.push(["setToolsExpanded", expanded]),
  };
  runner.setUIContext(ui, "tui");
  const live = runner.getUIContext();
  const dialogs = { select: await live.select("Pick", ["x", "y"]), confirm: await live.confirm("Sure?", "really"), input: await live.input("Name", "placeholder"), editor: await live.editor("Edit", "pre") };
  live.notify("hello", "info");
  live.setStatus("k", "v");
  live.setWorkingMessage("working");
  live.setWorkingVisible(false);
  live.setHiddenThinkingLabel("hidden");
  live.setWidget("w", ["line"], { placement: "belowEditor" });
  live.setTitle("title");
  live.pasteToEditor("pasted");
  live.setEditorText("set");
  live.setToolsExpanded(false);
  const reads = { editorText: live.getEditorText(), toolsExpanded: live.getToolsExpanded() };
  // The prompt events are delivered on a later turn: wait for both of the first dialog.
  for (let i = 0; i < 100 && log.filter(([name, type]) => name === "appendEntry" && type === "a_ui").length < 8; i++) await new Promise((resolve) => setTimeout(resolve, 50));
  r.ui = { mode: runner.createContext().mode, hasUI: runner.hasUI(), contextHasUI: runner.createContext().hasUI, dialogs, reads, calls: uiLog };
  runner.setUIContext(undefined);
  r.ui.afterClear = { hasUI: runner.hasUI(), mode: runner.createContext().mode };

  // Listeners and a stale runner.
  off();
  runner.emitError({ extensionPath: "/x/ext.mjs", event: "after-off", error: "not delivered" });
  r.errors = errors;
  runner.invalidate("runner is stale");
  runner.invalidate("a second message is ignored");
  r.stale = {
    cwd: await error(() => runner.createContext().cwd), isIdle: await error(() => runner.createContext().isIdle()), activeTools: await error(() => runner.getActiveTools()),
    commandContext: await error(() => runner.createCommandContext().waitForIdle()), toolContext: await error(() => runner.createToolContext("c", undefined).executeTool("alpha", {})),
    ui: await error(() => runner.createContext().ui), emit: await error(() => runner.emit({ type: "agent_start" })),
  };
  r.log = log;
  return r;
}
