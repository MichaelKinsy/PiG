import { modelFetchCallbacks } from "./model-fetch.mjs";
import { AutocompleteRuntime } from "./autocomplete.mjs";
import { dispatchFacet, dispatchFacetSync, hasFacetBridge } from "./facet-bridge.mjs";
import { syncTerminalGeometry } from "./terminal-geometry.mjs";
import { mountedOverlayHandle } from "./overlay-handle.mjs";
import { disposeIndependentSessions } from "./independent-session-owner.mjs";
import { dispatchNativeProvider, nativeDeclaration } from "./native-provider.mjs";
import { dispatchProviderObjectSync, dispatchProviderObjectCallback, remoteProvider } from "./provider-object.mjs";
import { isWaiting, ProviderSocket, setProviderSocketsRef } from "./provider-socket.mjs";
import { MessageChannel } from "node:worker_threads";
import net from "node:net";
import { closeSync, openSync, readSync } from "node:fs";
import { basename } from "node:path";
import { AsyncLocalStorage } from "node:async_hooks";
import { importExtension } from "./jiti-loader.mjs";
import { randomUUID } from "node:crypto";
import { createRequire, flushCompileCache } from "node:module";
import { stringWidget } from "./widget-component.mjs";
import { currentRuntime, runWithRuntime, setRuntime } from "./state.mjs";
import { getKeybindings, KeybindingsManager, setKeybindings } from "./shims/pi-dist/pi-tui/keybindings.js";
import { EditorComponentHost } from "./editor-component.mjs";
import { setCapabilities as setTerminalCapabilities } from "./shims/pi-dist/pi-tui/terminal-image.js";
import { loadAllHighlightLanguages } from "./shims/syntax-highlight.mjs";
import { themeFromPalette } from "./theme-palette.mjs";
import { initTheme, setThemeInstance } from "./shims/pi-dist/pi-coding-agent/modes/interactive/theme/theme.js";
import { createExtensionRuntime } from "./shims/pi-dist/pi-coding-agent/core/extensions/loader.js";
import { installCloneHooks, Realm } from "./xref.mjs";
import { SharedBus } from "./event-bus.mjs";

// Pi hands every extension one shared event bus (resource-loader.ts creates
// one per loader and passes it to each extension). Every Node realm of this
// Host shares it: this process's listeners stay in its own heap, and
// payloads cross to other realms by reference (xref.mjs).
const liveRuntimes = new Set();
const xrefRealm = new Realm(() => {
  const preferred = currentRuntime();
  for (const runtime of preferred ? [preferred, ...liveRuntimes] : liveRuntimes) {
    const connection = runtime?.connection();
    if (connection && !connection.closed) return runtime.xrefTransport;
  }
  return undefined;
});
installCloneHooks(xrefRealm);
const sharedBus = new SharedBus(xrefRealm, process.env.PIG_EVENT_BUS === "routed");
const XREF_REQUESTS = new Set(["xref.op", "events.dispatch", "events.migrate"]);
// Emissions whose foreign listener prefix ran while this process was idle. Pi runs such a listener's continuations only after the emitter's synchronous run, so this process waits for the emitter's first microtask (events.proceed) before its own microtasks run.
const lockstep = new Set();

// Marks the renderers of a definition from Pi's create<Tool>ToolDefinition
// (shims/builtin-tools.mjs): the host draws those halves with its built-in
// renderers for the named tool.
const builtInToolRenderer = Symbol.for("pig.builtInToolRenderer");

const USER_BLOCKING_CALLS = new Set(["ui.select", "ui.confirm", "ui.input", "ui.editor", "ui.custom"]);
const MAX_FRAME_SIZE = 128 * 1024 * 1024;
const REMOTE_RENDER_INTERVAL_MS = 16;

const BLOCKING_EVENTS = new Set([
  "input",
  "context",
  "context_with_system",
  "before_provider_request",
  "before_agent_start",
  "tool_call",
  "tool_result",
  "session_before_compact",
  "session_before_tree",
  "session_before_fork",
  "session_before_switch",
]);

function uuid(prefix = "c") {
  return `${prefix}${Math.random().toString(16).slice(2)}${Date.now().toString(16)}`;
}

// toolContent converts a tool result's content to the wire shape: a string
// stays a string, and upstream's (TextContent | ImageContent)[] passes through
// block by block, unchanged. Any other value becomes one text block of JSON.
function toolContent(content) {
  if (content == null) return "";
  if (typeof content === "string") return content;
  const items = Array.isArray(content) ? content : [content];
  return items.map((item) => {
    if (typeof item === "string") return { type: "text", text: item };
    if (item?.type === "text") return { type: "text", text: String(item.text ?? "") };
    if (item?.type === "image") return { type: "image", data: String(item.data ?? ""), mimeType: String(item.mimeType ?? "") };
    let text;
    try { text = JSON.stringify(item); } catch { text = String(item); }
    return { type: "text", text };
  });
}

function providerIDFromModel(model) {
  if (!model || typeof model !== "object") return "";
  if (typeof model.provider === "string") return model.provider;
  if (model.provider && typeof model.provider.id === "string") return model.provider.id;
  if (typeof model.id === "string" && model.id.includes("/")) return model.id.split("/", 1)[0];
  return "";
}

function modelIDFromModel(model) {
  if (!model || typeof model !== "object") return "";
  if (typeof model.modelId === "string") return model.modelId;
  if (typeof model.id !== "string") return "";
  const hasProvider = typeof model.provider === "string" || (model.provider && typeof model.provider.id === "string");
  if (hasProvider) return model.id;
  return model.id.includes("/") ? model.id.split("/").slice(1).join("/") : model.id;
}

function inferAPIForProvider(provider) {
  switch (provider) {
    case "anthropic": return "anthropic-messages";
    case "google": return "google-generative-ai";
    case "google-gemini-cli": return "google-gemini-cli";
    case "google-antigravity": return "google-gemini-cli";
    case "google-vertex": return "google-vertex";
    case "amazon-bedrock": return "bedrock-converse-stream";
    case "azure-openai": return "azure-openai-responses";
    case "openai-codex": return "openai-codex-responses";
    default: return "openai-responses";
  }
}

function modelRegistryKey(provider, modelId) {
  return `${provider}\u0000${modelId}`;
}

function normalizeModel(model) {
  if (!model || typeof model !== "object") return undefined;
  const provider = providerIDFromModel(model);
  const modelId = modelIDFromModel(model);
  if (!provider && !modelId) return undefined;
  return {
    ...model,
    id: modelId,
    modelId,
    provider,
    name: model.name ?? model.displayName ?? modelId,
    displayName: model.displayName ?? model.name ?? modelId,
    api: model.api ?? inferAPIForProvider(provider),
  };
}

// Pi's session projection and builtin provider modules load on first use:
// most extensions never read them, and loading them costs every extension
// process startup time. require() of an ES module is synchronous, as the
// ctx.sessionManager and ctx.modelRegistry reads that need them are.
const requireModule = createRequire(import.meta.url);
let piSessionModule;
function piSession() {
  piSessionModule ??= requireModule("./shims/pi-dist/pi-coding-agent/core/session-manager.js");
  return piSessionModule;
}
function piProviderComposer() {
  return requireModule("./shims/pi-dist/pi-coding-agent/core/provider-composer.js");
}
let piBuiltinProviderMap;
function piBuiltinProvider(id) {
  piBuiltinProviderMap ??= new Map(requireModule("./shims/pi-dist/pi-ai/sdk-bundle/providers.js").builtinProviders().map((provider) => [provider.id, provider]));
  return piBuiltinProviderMap.get(id);
}

// Upstream SessionManager's generateId: 8 hex characters, unique among the
// log's ids, else a full UUID.
function generateEntryId(byId) {
  for (let i = 0; i < 100; i++) {
    const id = randomUUID().slice(0, 8);
    if (!byId.has(id)) return id;
  }
  return randomUUID();
}

// 0.3.0: replaced by Pi runner wiring
// ctx.sessionManager: upstream hands extensions its SessionManager, typed as
// ReadonlySessionManager (session-manager.ts). The reads answer from the
// session log the host replicates into this process, through Pi's own
// projection code (pi-dist session-manager.js) and line-for-line ports of the
// SessionManager methods over it. The header facts (cwd, session directory,
// persistence, header) arrive with the host's state pushes.
class RuntimeSessionManager {
  constructor(runtime) { this.runtime = runtime; }

  // _entries is the replicated log followed by the entries appendCustomEntry
  // appended that the host has not yet sent back, so a read sees an append at
  // once, as upstream's in-process log does.
  _entries() {
    this.runtime.ensureSessionLog();
    const entries = this.runtime.state.session?.entries || [];
    const pending = this.runtime.pendingEntries;
    if (pending.length === 0) return entries;
    const replicated = new Map();
    entries.forEach((entry, index) => replicated.set(entry?.id, index));
    const unconfirmed = [];
    for (const entry of pending) {
      const index = replicated.get(entry.id);
      if (index === undefined) {
        unconfirmed.push(entry);
        continue;
      }
      // The host's copy holds the same values; keep the object the caller
      // holds, as upstream's log holds the entry it returned.
      entries[index] = entry;
    }
    if (unconfirmed.length !== pending.length) {
      this.runtime.pendingEntries = unconfirmed;
      this.runtime.branchCache = undefined;
    }
    return unconfirmed.length === 0 ? entries : entries.concat(unconfirmed);
  }

  // _index mirrors upstream's byId, labelsById and labelTimestampsById,
  // rebuilt when the log changes.
  _index() {
    const base = this.runtime.state.session?.entries;
    const entries = this._entries();
    const cached = this._cachedIndex;
    if (cached && cached.base === base && cached.entries.length === entries.length && cached.pending === this.runtime.pendingEntries.length) return cached;
    const byId = new Map();
    const labelsById = new Map();
    const labelTimestampsById = new Map();
    for (const entry of entries) {
      if (!entry || entry.type === "session") continue;
      byId.set(entry.id, entry);
      if (entry.type === "label") {
        if (entry.label) {
          labelsById.set(entry.targetId, entry.label);
          labelTimestampsById.set(entry.targetId, entry.timestamp);
        } else {
          labelsById.delete(entry.targetId);
          labelTimestampsById.delete(entry.targetId);
        }
      }
    }
    this._cachedIndex = { base, entries, pending: this.runtime.pendingEntries.length, byId, labelsById, labelTimestampsById };
    return this._cachedIndex;
  }

  _info() { return this.runtime.state.session?.info ?? {}; }

  // An empty host leaf is a reset to the root, not the last stored entry.
  _leafId() {
    this._entries();
    const pending = this.runtime.pendingEntries;
    if (pending.length > 0) return pending[pending.length - 1].id;
    return this.runtime.state.session?.leafId || null;
  }

  isPersisted() { return this._info().persisted ?? Boolean(this.runtime.state.session?.sessionFile); }
  getCwd() { return this._info().cwd ?? this.runtime.ctx.cwd; }
  getSessionDir() { return this._info().sessionDir ?? ""; }
  usesDefaultSessionDir() { return this._info().usesDefaultSessionDir === true; }
  getSessionId() { return this.runtime.state.session?.sessionId || ""; }
  getSessionFile() { return this.runtime.state.session?.sessionFile || undefined; }
  getSessionName() { return this.runtime.state.session?.sessionName?.trim() || undefined; }
  getLeafId() { return this._leafId(); }
  getLeafEntry() {
    const leafId = this._leafId();
    return leafId ? this._index().byId.get(leafId) : undefined;
  }
  getEntry(id) { return this._index().byId.get(id); }
  getChildren(parentId) {
    const children = [];
    for (const entry of this._index().byId.values()) {
      if (entry.parentId === parentId) children.push(entry);
    }
    return children;
  }
  getLabel(id) { return this._index().labelsById.get(id); }
  getBranch(fromId) {
    if (fromId === undefined && this.runtime.pendingEntries.length === 0) {
      // The first session read requests the log, whichever read it is.
      this._entries();
      return this.runtime.branchFromEntries();
    }
    const { byId } = this._index();
    const startId = fromId ?? this._leafId();
    const path = [];
    let current = startId ? byId.get(startId) : undefined;
    while (current) {
      path.push(current);
      current = current.parentId ? byId.get(current.parentId) : undefined;
    }
    path.reverse();
    return path;
  }
  buildContextEntries() {
    const { byId } = this._index();
    return piSession().buildContextEntries(this.getEntries(), this._leafId(), byId);
  }
  buildSessionProjection() {
    const { byId } = this._index();
    return piSession().buildSessionProjection(this.getEntries(), this._leafId(), byId);
  }
  buildSessionContext() {
    const { messages, thinkingLevel, model } = this.buildSessionProjection();
    return { messages, thinkingLevel, model };
  }
  getHeader() { return this._info().header ?? null; }
  getEntries() {
    return this._entries().filter((entry) => entry?.type !== "session");
  }
  getTree() {
    const { labelsById, labelTimestampsById } = this._index();
    const entries = this.getEntries();
    const nodeMap = new Map();
    const roots = [];
    for (const entry of entries) {
      const label = labelsById.get(entry.id);
      const labelTimestamp = labelTimestampsById.get(entry.id);
      nodeMap.set(entry.id, { entry, children: [], label, labelTimestamp });
    }
    for (const entry of entries) {
      const node = nodeMap.get(entry.id);
      if (entry.parentId === null || entry.parentId === entry.id) {
        roots.push(node);
      } else {
        const parent = nodeMap.get(entry.parentId);
        if (parent) parent.children.push(node);
        else roots.push(node);
      }
    }
    const stack = [...roots];
    while (stack.length > 0) {
      const node = stack.pop();
      node.children.sort((a, b) => new Date(a.entry.timestamp).getTime() - new Date(b.entry.timestamp).getTime());
      stack.push(...node.children);
    }
    return roots;
  }

  // appendCustomEntry is the write the host's session log takes from an
  // extension (upstream pi.appendEntry calls it). The id is generated here,
  // against the replicated log, so it returns synchronously as upstream's does.
  appendCustomEntry(customType, data) {
    const entry = {
      type: "custom",
      customType,
      data,
      id: generateEntryId(this._index().byId),
      parentId: this._leafId(),
      timestamp: new Date().toISOString(),
    };
    this.runtime.pendingEntries = [...this.runtime.pendingEntries, entry];
    this.runtime.fireAndForget("appendEntry", { customType, data, direct: { id: entry.id, timestamp: entry.timestamp } });
    return entry.id;
  }
}

// 0.3.0: replaced by Pi runner wiring
// ctx.modelRegistry: upstream's ModelRegistry facade over the session's
// ModelRuntime (model-registry.ts). Its synchronous reads answer from the
// registry snapshot the host pushes (model_registry_update); refresh,
// getProviderAuth and request auth ask the host; registrations go to the host,
// which shares them with every extension as upstream's one runtime does.
class RuntimeModelRegistry {
  constructor(runtime) { this.runtime = runtime; }

  _providerState(provider) { return this.runtime.registryState.providers?.[provider]; }

  async refresh(options = {}) {
    const result = await this.runtime.call("refreshModelRegistry", {
      allowNetwork: options?.allowNetwork,
      providers: options?.providers,
      force: options?.force,
    });
    if (result?.state) this.runtime.applyModelRegistryState(result.state);
    const errors = new Map();
    for (const [provider, message] of Object.entries(result?.errors ?? {})) errors.set(provider, new Error(message));
    return { aborted: result?.aborted === true || options?.signal?.aborted === true, errors };
  }
  getError() { return this.runtime.registryState.error || undefined; }
  getAll() { return [...this.runtime.models.values()]; }
  getAvailable() { return this.getAll().filter((model) => {const state=this._providerState(model.provider);return state?.configured===true&&(!state.availableModelIds||state.availableModelIds.includes(model.id))}); }
  find(provider, modelId) {
    return this.runtime.getModel(provider, modelId);
  }
  hasConfiguredAuth(model) { return this._providerState(model?.provider)?.configured === true; }
  async getApiKeyAndHeaders(model) {
    return this.runtime.call("getModelAuth", { provider: providerIDFromModel(model), modelId: modelIDFromModel(model) });
  }
  getProviderAuthStatus(provider) {
    const status = this._providerState(provider)?.authStatus;
    return status ? { ...status } : { configured: false };
  }
  getProvider(provider) { return this.runtime.getRegistryProvider(provider); }
  stream(model, context, options = {}) {
    return this.runtime.startModelStream(model, context, options);
  }
  streamSimple(model, context, options = {}) {
    return this.runtime.startModelStream(model, context, options,true);
  }
  complete(model, context, options = {}) {
    return this.stream(model, context, options).result();
  }
  getProviderDisplayName(provider) { return this._providerState(provider)?.name ?? this.getProvider(provider)?.name ?? provider; }
  async getProviderAuth(provider) {
    return (await this.runtime.call("getProviderAuth", { provider })) ?? undefined;
  }
  async getApiKeyForProvider(provider) {
    try {
      return (await this.getProviderAuth(provider))?.auth.apiKey;
    } catch {
      return undefined;
    }
  }
  isUsingOAuth(model) { return this._providerState(model?.provider)?.usingOAuth === true; }
  registerProvider(providerOrName, config) {
    if (typeof providerOrName === "string") {
      if (!config) throw new Error("Provider config is required when registering by name");
      this.runtime.registerProvider(providerOrName, config);
      return;
    }
    this.runtime.registerNativeProvider(providerOrName);
  }
  unregisterProvider(providerName) { this.runtime.unregisterProvider(providerName); }
  getRegisteredProviderConfig(providerName) {
    const local = this.runtime.registeredProviderConfig(providerName);
    if (local) return local;
    const entry = this.runtime.registryState.registered?.find((candidate) => candidate.name === providerName);
    if (!entry?.config) return undefined;
    const foreign = this.runtime.foreignProviderConfig(providerName);
    if (foreign) return foreign;
    // pig divergence (D78): a registration from a native SDK author is snapshot data, not the author's live configuration object.
    return entry.config;
  }
  // Native Providers remain local when their owner shares the cell; other owners expose connection-bound method handles.
  getRegisteredNativeProvider(providerName) {
    const native = this.runtime.nativeProviderObjects.get(providerName)?.provider;
    if (native) return native;
    const declaration = this.runtime.registryState.registered?.find(entry => entry.name === providerName)?.native;
    return declaration ? remoteProvider(this.runtime, declaration, ModelEventStream) : undefined;
  }
  getRegisteredProviderIds() {
    return [...new Set([
      ...(this.runtime.registryState.registered ?? []).map((entry) => entry.name),
      ...(this.runtime.loadState === "active" ? this.runtime.providerConfigObjects.keys() : this.runtime.registeredProviderConfigs.keys()),
      ...this.runtime.nativeProviders.keys(),
    ])];
  }
}

export class ThemeShim {
  constructor() {
    this.foregrounds = {};
    this.backgrounds = {};
    // Upstream's bold, italic, underline, inverse and strikethrough are chalk
    // styles, which chalk drops when the host's stdout has no color support.
    this.modifiers = true;
  }
  setPalette(palette = {}) {
    this.foregrounds = palette.foregrounds && typeof palette.foregrounds === "object" ? palette.foregrounds : {};
    this.backgrounds = palette.backgrounds && typeof palette.backgrounds === "object" ? palette.backgrounds : {};
    this.modifiers = palette.modifiers !== false;
    this.name = typeof palette.name === "string" ? palette.name : undefined;
    this.sourcePath = typeof palette.sourcePath === "string" && palette.sourcePath !== "" ? palette.sourcePath : undefined;
    this.mode = palette.mode === "256color" ? "256color" : "truecolor";
    setThemeInstance(this);
  }
  style(open, close, text) {
    const value = String(text ?? "");
    return this.modifiers ? `${open}${value}${close}` : value;
  }
  fg(token, text) {
    const value = String(text ?? "");
    const open = this.foregrounds[token] || "";
    return open ? `${open}${value}\x1b[39m` : value;
  }
  bg(token, text) {
    const value = String(text ?? "");
    const open = this.backgrounds[token] || "";
    return open ? `${open}${value}\x1b[49m` : value;
  }
  bold(text) { return this.style("\x1b[1m", "\x1b[22m", text); }
  dim(text) { return `\x1b[2m${String(text ?? "")}\x1b[22m`; }
  italic(text) { return this.style("\x1b[3m", "\x1b[23m", text); }
  underline(text) { return this.style("\x1b[4m", "\x1b[24m", text); }
  inverse(text) { return this.style("\x1b[7m", "\x1b[27m", text); }
  strikethrough(text) { return this.style("\x1b[9m", "\x1b[29m", text); }
  // Upstream Theme.getFgAnsi, getBgAnsi, getColorMode, getThinkingBorderColor
  // and getBashModeBorderColor over the host's palette.
  getFgAnsi(color) {
    const ansi = this.foregrounds[color];
    if (!ansi) throw new Error(`Unknown theme color: ${color}`);
    return ansi;
  }
  getBgAnsi(color) {
    const ansi = this.backgrounds[color];
    if (!ansi) throw new Error(`Unknown theme background color: ${color}`);
    return ansi;
  }
  getColorMode() { return this.mode ?? "truecolor"; }
  getThinkingBorderColor(level) {
    const token = { off: "thinkingOff", minimal: "thinkingMinimal", low: "thinkingLow", medium: "thinkingMedium", high: "thinkingHigh", xhigh: "thinkingXhigh", max: "thinkingMax" }[level] ?? "thinkingOff";
    return (text) => this.fg(token, text);
  }
  getBashModeBorderColor() { return (text) => this.fg("bashMode", text); }
}

// pig additive (D19): after RPC stdin ends, the transport must not keep an otherwise drained Node event loop alive. Report the drain to the process owner without resolving any pending extension Promise.
const quitDrain = { active: false, inputEnded: false, reported: false, tailCheck: false, runtimes: new Set() };

function quitDrainState() {
  let outstanding = false;
  let tail = false;
  let hostWaits = false;
  for (const runtime of quitDrain.runtimes) {
    for (const request of runtime.requestRecords.values()) {
      if (request.responded) continue;
      outstanding ||= request.quit;
      tail ||= request.quitTail;
    }
    for (const pending of runtime.conn?.pending.values() ?? []) {
      if (pending.synchronous || !USER_BLOCKING_CALLS.has(pending.method)) hostWaits = true;
    }
  }
  return { outstanding, tail, hostWaits };
}

function refreshQuitDrain() {
  if (!quitDrain.active || !quitDrain.inputEnded || quitDrain.reported) return;
  const { outstanding, tail, hostWaits } = quitDrainState();
  setProviderSocketsRef(!outstanding || hostWaits);
  // Pi flushRawStdout lets immediately fulfilled Promise continuations run, but does not await a post-disposal handler's future timer or I/O. IPC must not turn that suspension into a Session join.
  if (tail && !outstanding && !hostWaits && !quitDrain.tailCheck) {
    quitDrain.tailCheck = true;
    setImmediate(() => {
      quitDrain.tailCheck = false;
      const current = quitDrainState();
      if (current.tail && !current.outstanding && !current.hostWaits) reportQuitExit("runtime_quit_yield");
    });
  }
}

// pig additive (D19): Pi reads the prompt line, and stdin's end one event-loop iteration later; then shutdown() exits within microtasks and one tick unless a session_shutdown handler awaits a timer or I/O (rpc-mode.ts:728-744, output-guard.ts:105-108). A command therefore answers after stdin ended only if it settles inside its own window: its microtasks and the setImmediate queued behind its handler's synchronous prefix. The runtime measures that window from the invocation and reports the request suspended (request_state) when the window closes unresponded; the host decides what stdin's end makes of that: it holds a suspended command's response unless a quit session_shutdown handler is suspended too, which keeps Pi's process alive. Only a host call that settles synchronously or through microtasks in Pi keeps a window open; these host calls are I/O, a child process, a dialog or a session change there.
const WINDOW_CLOSING_CALLS = new Set([
  ...USER_BLOCKING_CALLS, "exec", "modelStream", "getModelAuth", "getProviderAuth", "refreshModelRegistry", "compact", "waitForIdle",
  "setModel", "newSession", "fork", "navigateTree", "switchSession", "reload", "oauth.cb.onSelect", "oauth.cb.onPrompt", "oauth.cb.onManualCodeInput",
]);

function windowPending(owner) {
  for (const pending of owner.connection.pending.values()) {
    if (pending.parentRequestId === owner.id && !WINDOW_CLOSING_CALLS.has(pending.method)) return true;
  }
  return false;
}

// armRequestWindow closes the window of a command or of a quit session_shutdown handler at the next check phase after its handler's synchronous prefix, or once its pending short host calls settle. A command arms it before its handler runs with early set: its immediate is then queued ahead of every immediate the handler and its microtask continuations queue in this iteration, and it closes a MessagePort, whose close callback runs in this iteration's closing phase, after the check phase and before the next poll phase. A threadpool completion, a nested immediate and a timer therefore land after the window closes.
function armRequestWindow(owner, early = false) {
  const close = () => {
    if (owner.responded || owner.turned) return;
    if (windowPending(owner)) {
      owner.windowWait = true;
      return;
    }
    owner.turned = true;
    if (!owner.connection.closed) owner.connection.requestState(owner.id, "suspended");
  };
  if (early) {
    closeInCloseCallbacks(close);
  } else setImmediate(close);
}

// closeInCloseCallbacks runs fn in the closing phase of the current loop iteration: after the iteration's check-phase immediates and before the next timers and poll. It closes a MessagePort, a libuv handle whose 'close' event fires from the handle's close callback. The port is unref'd and opens no fd or socket. Verified on Node 22, 24 and 25. If the mechanism throws, fn runs two immediates later, the previous behaviour, and the failure never reaches the command. fn runs exactly once.
export function closeInCloseCallbacks(fn, factory = () => new MessageChannel()) {
  let port;
  try {
    port = factory().port1;
    if (typeof port?.once !== "function" || typeof port?.close !== "function") throw new TypeError("no message port");
  } catch {
    setImmediate(() => setImmediate(fn));
    return;
  }
  setImmediate(() => {
    try {
      port.once("close", fn);
      port.close();
    } catch {
      setImmediate(fn);
    }
  });
}

function resumeRequestWindows() {
  for (const runtime of quitDrain.runtimes) {
    for (const request of runtime.requestRecords.values()) {
      if (request.windowWait && !windowPending(request)) {
        request.windowWait = false;
        armRequestWindow(request);
      }
    }
  }
}

function drainQuitRequests() {
  const { outstanding, hostWaits } = quitDrainState();
  if (outstanding && !hostWaits) reportQuitExit("runtime_drained");
}

function reportQuitExit(method) {
  if (!quitDrain.active || !quitDrain.inputEnded || quitDrain.reported) return;
  quitDrain.reported = true;
  setProviderSocketsRef(true);
  for (const runtime of quitDrain.runtimes) {
    if (runtime.conn) {
      runtime.conn.notify(method);
      break;
    }
  }
}

function enterQuitDrain() {
  if (quitDrain.active) return;
  quitDrain.active = true;
  process.on("beforeExit", drainQuitRequests);
}

export class Connection {
  constructor(socket) {
    this.socket = socket;
    this.pending = new Map();
    this.queue = [];
    this.waiters = [];
    this.closed = false;
    this.streamStarts = new Map();
    socket.on("envelope", env => this.onEnvelope(env));
    socket.on("close", () => this.onClose());
    socket.on("error", () => this.onClose());
  }

  onClose() {
    if (this.closed) return;
    this.closed = true;
    for (const waiter of this.waiters.splice(0)) waiter(null);
    for (const [id, pending] of this.pending) {
      // A received result still belongs to the ordered receive loop, even if the peer closes before that loop reaches it.
      if (pending.responseQueued) continue;
      this.pending.delete(id);
      pending.reject(new Error("connection closed"));
    }
  }

  onEnvelope(env) {
    // Cross-process reference operations and bus dispatches are restricted synchronous requests: they run whether this process is idle or waiting in a synchronous host call, at any nesting depth.
    if (env.type === "request" && XREF_REQUESTS.has(env.request?.method)) {
      this.xrefHandler(env);
      return;
    }
    if (env.type === "notify" && ["xref.unpin", "events.release", "events.proceed"].includes(env.notify?.method)) {
      if (env.notify.method === "xref.unpin") xrefRealm.unpin(env.notify.args ?? {});
      else if (env.notify.method === "events.proceed") lockstep.delete(env.notify.args?.emission);
      else sharedBus.release(env.notify.args ?? {});
      return;
    }
    if (env.type === "request" && ["provider_sync", "provider_object_callback_sync", "autocomplete.sync", "facet_sync"].includes(env.request?.method)) {
      this.requestState(env.id, "started");
      try { this.respond(env.id, this.syncHandler(env.request)); }
      catch (error) { this.respond(env.id, null, error); }
      return;
    }
    if (env.type === "cancel") this.cancelParent(env.cancel?.request_id || env.id || "", true);
    if (env.type === "notify" && env.notify?.method === "model_stream_event" && env.notify.args?.started) {
      this.streamStarts.get(env.notify.args.streamId)?.();
      return;
    }
    if (env.type === "call_result") {
      const pending = this.pending.get(env.id);
      if (pending?.synchronous) {
        this.resolveCall(env);
        return;
      }
      if (pending) pending.responseQueued = true;
    }
    if (this.waiters.length > 0) this.waiters.shift()(env);
    else this.queue.push(env);
  }

  resolveCall(env) {
    const pending = this.pending.get(env.id);
    if (!pending) return;
    this.pending.delete(env.id);
    resumeRequestWindows();
    refreshQuitDrain();
    if (env.call_result?.error) {
      const info = env.call_result.error;
      const error = new Error(info.code ? `${info.code}: ${info.message}` : info.message);
      error.code = info.code || "";
      pending.reject(error);
    } else pending.resolve(env.call_result?.result ?? null);
  }

  onDrain(handler) {
    this.socket.on("drain", handler);
    return () => this.socket.off("drain", handler);
  }

  send(env) {
    const data = Buffer.from(JSON.stringify(env));
    if (data.length > MAX_FRAME_SIZE) {
      throw new Error(`frame too large: ${data.length} bytes exceeds ${MAX_FRAME_SIZE}`);
    }
    const hdr = Buffer.alloc(4);
    hdr.writeUInt32BE(data.length, 0);
    return this.socket.write(Buffer.concat([hdr, data]));
  }

  next() {
    if (this.queue.length > 0) return Promise.resolve(this.queue.shift());
    if (this.closed) return Promise.resolve(null);
    return new Promise((resolve) => this.waiters.push(resolve));
  }

  call(method, args = {}, parentRequestId = "") {
    if (this.closed) return Promise.reject(new Error("extension connection closed"));
    const id = uuid();
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject, parentRequestId, method });
      refreshQuitDrain();
      try {
        this.send({ type: "call", id, call: { method, args, ...(parentRequestId ? { parent_request_id: parentRequestId } : {}) } });
      } catch (error) {
        this.pending.delete(id);
        reject(error);
      }
    });
  }

  callSync(method, args = {}, parentRequestId = "") {
    const id = uuid();
    let done = false, value, error;
    this.pending.set(id, {
      synchronous: true,
      parentRequestId,
      resolve: result => { value = result; done = true; },
      reject: cause => { error = cause; done = true; },
    });
    this.send({ type: "call", id, call: { method, args, parent_request_id: parentRequestId } });
    try { this.socket.waitUntil(() => done); }
    finally { this.pending.delete(id); }
    if (error) throw error;
    return value;
  }

  callWithStreamStart(method, args, parentRequestId) {
    let started = false, startError;
    this.streamStarts.set(args.streamId, () => { started = true; });
    const id = uuid();
    const promise = new Promise((resolve, reject) => {
      this.pending.set(id, {
        synchronous: true,
        parentRequestId,
        resolve: result => { if (!started) startError = new Error("Provider ended without starting its stream"); started = true; resolve(result); },
        reject: error => { if (!started) startError = error; started = true; reject(error); },
      });
    });
    // A synchronous start failure is thrown below; the rejected completion is still observed.
    void promise.catch(() => {});
    this.send({ type: "call", id, call: { method, args, parent_request_id: parentRequestId } });
    try { this.socket.waitUntil(() => started); }
    finally { this.streamStarts.delete(args.streamId); }
    if (startError) throw startError;
    return promise;
  }

  cancelParent(parentRequestId, synchronousOnly = false) {
    for (const [id, pending] of this.pending) {
      if (pending.parentRequestId !== parentRequestId || (synchronousOnly && !pending.synchronous)) continue;
      this.pending.delete(id);
      pending.reject(new Error(`host call cancelled with parent request ${parentRequestId}`));
    }
  }

  notify(method, args = {}) {
    // Fire-and-forget Node→Go notification: no id, no pending entry.
    this.send({ type: "notify", notify: { method, args } });
  }

  respond(id, result = null, error = null) {
    this.requestState(id, "completed");
    this.send({
      type: "response",
      id,
      response: error ? { result, error: errorInfo(error) } : { result },
    });
  }

  requestState(id, state, reason = undefined) {
    this.send({
      type: "request_state",
      request_state: { request_id: id, state, ...(reason ? { reason } : {}) },
    });
  }

  // width is the terminal width the lines were rendered at; the host never
  // paints a frame rendered for another width.
  pushWidget(key, lines, width) {
    this.send({ type: "widget_push", widget_push: { key, lines, width } });
  }
}

// errorInfo is the wire form of a thrown error. The stack travels with it
// because upstream reports a handler's `err.stack` alongside its message.
export function errorInfo(error) {
  const info = { message: error?.message || String(error) };
  if (typeof error?.stack === "string" && error.stack !== "") info.stack = error.stack;
  return info;
}

// bindOwnMethods copies each prototype method onto the instance, bound to it.
// Upstream builds ExtensionContext (runner.ts createContext) and
// ExtensionUIContext (interactive-mode.ts createExtensionUIContext) as object
// literals of arrow functions, so a method still works when detached:
// pi-lens does `const setWidget = ui.setWidget; setWidget(...)`.
function bindOwnMethods(target) {
  const proto = Object.getPrototypeOf(target);
  for (const name of Object.getOwnPropertyNames(proto)) {
    if (name === "constructor") continue;
    const descriptor = Object.getOwnPropertyDescriptor(proto, name);
    if (typeof descriptor?.value === "function") target[name] = descriptor.value.bind(target);
  }
}

class RuntimeContext {
  constructor(runtime) {
    bindOwnMethods(this);
    this.runtime = runtime;
    Object.defineProperty(this, "scopedModels", { enumerable: true, get: () => runtime.state.scopedModels ?? [] });
    Object.defineProperties(this, {
      ui: { enumerable: true, get: () => runtime.state.hasUI ? runtime.ui : runtime.noOpUI },
      hasUI: { enumerable: true, get: () => runtime.state.hasUI },
    });
    this.cwd = runtime.ready.cwd || process.cwd();
    // Pi defaults to print until the host binds a mode (runner.ts:357).
    this.mode = runtime.ready.mode || "print";
    this.theme = this.ui.theme;
    this.model = normalizeModel(runtime.state.model) ?? (runtime.ready.model ? normalizeModel({ id: runtime.ready.model }) : undefined);
    this.sessionManager = new RuntimeSessionManager(runtime);
    this.modelRegistry = new RuntimeModelRegistry(runtime);
    this.signal = undefined;
  }

  // Live terminal geometry. Getters rather than snapshots so a resize is
  // visible without rebuilding the context. width mirrors the host's
  // width_change notification; height mirrors height_change.
  get width() { return Number(this.runtime.ready?.width || 0); }
  get height() { return Number(this.runtime.ready?.height || 0); }

  isIdle() { return this.runtime.state.isIdle; }
  isProjectTrusted() { return this.runtime.state.projectTrusted; }
  abort() { this.runtime.fireAndForget("abort", {}); }
  hasPendingMessages() { return this.runtime.state.hasPendingMessages; }
  // Pi's shutdown() stops the TUI only after the frame the current input
  // produced is painted (it drains input first), so a shutdown requested
  // from an editor's handleInput reaches the host after that frame.
  shutdown() { queueMicrotask(() => this.runtime.fireAndForget("shutdown", {})); }
  getContextUsage() { return this.runtime.state.contextUsage; }
  // The host reads the options flat. Upstream starts compaction without
  // awaiting it and reports the outcome through onComplete/onError, so a call
  // with callbacks carries no parent request and outlives its handler.
  compact(options = {}) {
    const { onComplete, onError, ...rest } = options ?? {};
    if (typeof onComplete !== "function" && typeof onError !== "function") {
      this.runtime.fireAndForget("compact", rest);
      return;
    }
    void this.runtime.conn?.call("compact", { ...rest, awaitCompletion: true }).then(
      (result) => { if (typeof onComplete === "function") onComplete(result); },
      (err) => { if (typeof onError === "function") onError(err instanceof Error ? err : new Error(String(err))); },
    );
  }
  getSystemPrompt() { return this.runtime.state.systemPrompt; }
  getSystemPromptOptions() { return this.runtime.state.systemPromptOptions ?? {}; }
  get thinkingLevel() { return this.runtime.state.thinkingLevel || undefined; }
  async waitForIdle() { return this.runtime.call("waitForIdle", {}); }
  async newSession(options = {}) { return this.runtime.call("newSession", { ...options }); }
  async fork(entryId, options = {}) { return this.runtime.call("fork", { ...options, entryId }); }
  async navigateTree(targetId, options = {}) { return this.runtime.call("navigateTree", { ...options, targetId }); }
  async switchSession(sessionPath, options = {}) { return this.runtime.call("switchSession", { sessionPath, options }); }
  async reload() { return this.runtime.call("reload", {}); }
}

// Ports packages/coding-agent/src/core/extensions/runner.ts: noOpUIContext.
function createNoOpUI(ui) {
  return {
    select: async () => undefined,
    confirm: async () => false,
    input: async () => undefined,
    notify: () => {},
    onTerminalInput: () => () => {},
    setStatus: () => {},
    setWorkingMessage: () => {},
    setWorkingVisible: () => {},
    setWorkingIndicator: () => {},
    setHiddenThinkingLabel: () => {},
    setWidget: () => {},
    setFooter: () => {},
    setHeader: () => {},
    setTitle: () => {},
    custom: async () => undefined,
    pasteToEditor: () => {},
    setEditorText: () => {},
    getEditorText: () => "",
    editor: async () => undefined,
    addAutocompleteProvider: () => {},
    setEditorComponent: () => {},
    getEditorComponent: () => undefined,
    get theme() { return ui.theme; },
    getAllThemes: () => [],
    getTheme: () => undefined,
    setTheme: () => ({ success: false, error: "UI not available" }),
    getToolsExpanded: () => false,
    setToolsExpanded: () => {},
    // pig additive (D60): login validation remains owned by the host.
    setLogin: ui.setLogin,
    onWidthChange: ui.onWidthChange,
  };
}

class RuntimeUI {
  constructor(runtime) {
    bindOwnMethods(this);
    this.runtime = runtime;
    this.theme = new ThemeShim();
  }
  async select(title, options, opts = {}) {
    this.runtime.blockForUser();
    const result = await this.runtime.call("ui.select", { title, options, opts });
    return result?.ok ? result.selected : undefined;
  }
  async confirm(title, message, opts = {}) {
    this.runtime.blockForUser();
    const result = await this.runtime.call("ui.confirm", { title, message, opts });
    return Boolean(result?.confirmed);
  }
  async input(title, placeholder = "", opts = {}) {
    this.runtime.blockForUser();
    const result = await this.runtime.call("ui.input", { title, placeholder, opts });
    return result?.ok ? result.text : undefined;
  }
  notify(message, type = "info") { this.runtime.fireAndForget("ui.notify", { message, level: type }); }
  onTerminalInput(handler) {
    if (typeof handler !== "function") {
      throw new TypeError("onTerminalInput requires a handler function");
    }
    return this.runtime.addTerminalInputHandler(handler);
  }
  // Upstream Pi installs headers and footers as component factories whose
  // render(width) runs every frame, so they follow a resize on their own. A pig
  // extension is a subprocess and sends static lines, so a footer keeps the
  // width it was built for until something re-pushes it. This is that trigger.
  // The handler runs after `width` is updated, so it observes the new value.
  onWidthChange(handler) {
    if (typeof handler !== "function") {
      throw new TypeError("onWidthChange requires a handler function");
    }
    return this.runtime.addWidthChangeHandler(handler);
  }
  setStatus(key, text) {
    // Upstream mutates FooterDataProvider synchronously before requesting a
    // render. Keep the subprocess replica current before sending the host call
    // so a footer installed on the next statement sees this status exactly
    // once and an existing footer can republish from the new snapshot.
    const footerData = this.runtime.state.footerData ??= {
      gitBranch: "", extensionStatuses: {}, availableProviderCount: 0,
    };
    const statuses = footerData.extensionStatuses ??= {};
    if (text === undefined || text === "") delete statuses[key];
    else statuses[key] = String(text);
    this.runtime.fireAndForget("ui.setStatus", { key, text });
    if (this.runtime.footerFactory) this.runtime.renderSpecialSurface("footer");
  }
  setWorkingMessage(message) { this.runtime.fireAndForget("ui.setWorkingMessage", { message }); }
  setWorkingIndicator(options = {}) { this.runtime.fireAndForget("ui.setWorkingIndicator", options); }
  setWorkingVisible(visible) { this.runtime.fireAndForget("ui.setWorkingVisible", { visible }); }
  setHiddenThinkingLabel(label) { this.runtime.fireAndForget("ui.setHiddenThinkingLabel", { label }); }
  setWidget(key, content, options = {}) {
    if (content === undefined || content === null) {
      this.runtime.clearWidget(key);
      return;
    }
    // 0.3.0: replaced by Pi runner wiring
    if (Array.isArray(content)) {
      if (this.runtime.ctx.mode !== "tui") {
        this.runtime.fireAndForget("ui.setWidget", { key, content, options });
        return;
      }
      this.runtime.setWidgetFactory(key, (_tui, theme) => stringWidget(content, theme), options);
      return;
    }
    if (typeof content === "function") {
      this.runtime.setWidgetFactory(key, content, options);
      return;
    }
    throw new Error("setWidget content must be a component factory, string array, or undefined");
  }
  setFooter(factory) {
    this.runtime.specialSurfaceComponents.get("footer")?.dispose?.();
    this.runtime.footerFactory = typeof factory === "function" ? factory : undefined;
    this.runtime.specialSurfaceComponents.delete("footer");
    if (this.runtime.footerFactory) this.runtime.renderSpecialSurface("footer");
    else this.runtime.fireAndForget("ui.setFooter", { clear: true });
  }
  setHeader(factory) {
    this.runtime.specialSurfaceComponents.get("header")?.dispose?.();
    this.runtime.headerFactory = typeof factory === "function" ? factory : undefined;
    this.runtime.specialSurfaceComponents.delete("header");
    if (this.runtime.headerFactory) this.runtime.renderSpecialSurface("header");
    else this.runtime.fireAndForget("ui.setHeader", { clear: true });
  }
  // pig additive (D60): Pig accepts typed login data across the subprocess wire.
  async setLogin(definition) { return this.runtime.call("ui.setLogin", definition); }
  setTitle(title) { this.runtime.fireAndForget("ui.setTitle", { title }); }
  async custom(factory, options = {}) {
    this.runtime.blockForUser();
    return this.runtime.openCustomOverlay(factory, options);
  }
  pasteToEditor(text) { this.runtime.fireAndForget("ui.pasteToEditor", { text }); }
  setEditorText(text) { this.runtime.fireAndForget("ui.setEditorText", { text }); }
  getEditorText() { return this.runtime.state.editorText ?? ""; }
  async editor(title, prefill = "") {
    this.runtime.blockForUser();
    const result = await this.runtime.call("ui.editor", { title, prefill });
    return result?.ok ? result.text : undefined;
  }
  addAutocompleteProvider(factory) { this.runtime.autocomplete.add(factory); }
  // 0.3.0: replaced by Pi runner wiring
  // Pi's setEditorComponent: the factory's editor replaces the host's editor
  // (editor-component.mjs); undefined restores it.
  setEditorComponent(factory) {
    if (typeof factory !== "function") {
      this.runtime.editorFactory = undefined;
      this.runtime.editorHost.clear();
      return;
    }
    this.runtime.editorFactory = factory;
    this.runtime.editorHost.install(factory);
  }
  getEditorComponent() { return this.runtime.editorFactory; }
  getAllThemes() { return this.runtime.state.allThemes ?? []; }
  getTheme(name) {
    return themeFromPalette(this.runtime.callSync("ui.getTheme", { name })?.theme);
  }
  setTheme(name) {
    const result = this.runtime.callSync("ui.setTheme", { theme: name });
    const palette = this.runtime.callSync("ui.theme")?.theme;
    if (palette) this.theme.setPalette(palette);
    return result;
  }
  getToolsExpanded() { return this.runtime.state.toolsExpanded ?? false; }
  setToolsExpanded(expanded) { this.runtime.fireAndForget("ui.setToolsExpanded", { expanded }); }
}

// serializeOverlayOptions keeps the pi-tui OverlayOptions fields that can
// cross the process boundary (everything except the visible callback).
function serializeOverlayOptions(opts) {
  if (!opts || typeof opts !== "object") return undefined;
  const out = {};
  const size = (v) => (typeof v === "number" && Number.isFinite(v)) || typeof v === "string";
  for (const k of ["width", "maxHeight", "row", "col"]) if (size(opts[k])) out[k] = opts[k];
  for (const k of ["minWidth", "offsetX", "offsetY"]) {
    if (typeof opts[k] === "number" && Number.isFinite(opts[k])) out[k] = opts[k];
  }
  if (typeof opts.anchor === "string") out.anchor = opts.anchor;
  if (typeof opts.margin === "number" && Number.isFinite(opts.margin)) out.margin = opts.margin;
  else if (opts.margin && typeof opts.margin === "object") {
    const m = {};
    for (const k of ["top", "right", "bottom", "left"]) if (typeof opts.margin[k] === "number") m[k] = opts.margin[k];
    out.margin = m;
  }
  if (opts.nonCapturing) out.nonCapturing = true;
  return Object.keys(out).length ? out : undefined;
}

function parseOverlaySize(value, reference) {
  if (value === undefined) return undefined;
  if (typeof value === "number") return value;
  const match = String(value).match(/^(\d+(?:\.\d+)?)%$/);
  if (match) return Math.floor((reference * parseFloat(match[1])) / 100);
  return undefined;
}

// resolveOverlayWidth is upstream TUI.resolveOverlayLayout's width rule; the
// host compositor (tui.resolveOverlayLayout) resolves the same value.
function resolveOverlayWidth(opts, termWidth) {
  const margin = typeof opts.margin === "number"
    ? { top: opts.margin, right: opts.margin, bottom: opts.margin, left: opts.margin }
    : (opts.margin ?? {});
  const availWidth = Math.max(1, termWidth - Math.max(0, margin.left ?? 0) - Math.max(0, margin.right ?? 0));
  let width = parseOverlaySize(opts.width, termWidth) ?? Math.min(80, availWidth);
  if (opts.minWidth !== undefined) width = Math.max(width, opts.minWidth);
  return Math.trunc(Math.max(1, Math.min(width, availWidth)));
}

// OAuthBridgeCancelled aborts a value-returning login callback when the user
// dismissed the host prompt. It mirrors the Go/Rust/Python SDK cancel sentinels.
class OAuthBridgeCancelled extends Error {
  constructor() {
    super("oauth prompt cancelled");
    this.name = "OAuthBridgeCancelled";
  }
}

// Credentials are Pi's complete token objects: provider-owned keys and the exact expires value cross unchanged. A nullish result spreads to an empty object, as `{ ...result, type: "oauth" }` does in provider-composer.ts:289-292.
function oauthCredsToWire(creds) {
  return { ...creds };
}

function oauthCredsFromWire(wire) {
  return { ...wire };
}

// OAuthLoginCallbacks presents the upstream config.oauth callback surface to a
// provider's login() closure and forwards each call to the host over oauth.cb.*.
// Value-returning callbacks resolve the host reply; a host cancel becomes an
// undefined return (onSelect) or a thrown cancel (onPrompt/onManualCodeInput),
// matching the upstream in-process callback semantics.
class OAuthLoginCallbacks {
  constructor(runtime, signal) {
    this.runtime = runtime;
    this.signal = signal;
  }
  onAuth(info) {
    info = info || {};
    this.runtime.fireAndForget("oauth.cb.onAuth", { url: info.url ?? "", instructions: info.instructions });
  }
  onDeviceCode(info) {
    info = info || {};
    this.runtime.fireAndForget("oauth.cb.onDeviceCode", {
      userCode: info.userCode ?? "",
      verificationUri: info.verificationUri ?? "",
      intervalSeconds: info.intervalSeconds,
      expiresInSeconds: info.expiresInSeconds,
    });
  }
  onProgress(message) {
    this.runtime.fireAndForget("oauth.cb.onProgress", { message: String(message ?? "") });
  }
  async onPrompt(prompt) {
    prompt = prompt || {};
    const result = await this.runtime.call("oauth.cb.onPrompt", {
      message: prompt.message ?? "",
      placeholder: prompt.placeholder,
      allowEmpty: prompt.allowEmpty,
    });
    if (result?.cancel) throw new OAuthBridgeCancelled();
    return result?.value ?? "";
  }
  async onSelect(prompt) {
    prompt = prompt || {};
    const result = await this.runtime.call("oauth.cb.onSelect", {
      message: prompt.message ?? "",
      options: (prompt.options ?? []).map((o) => ({ id: o.id, label: o.label })),
    });
    if (result?.cancel) return undefined;
    return result?.value ?? "";
  }
  async onManualCodeInput() {
    const result = await this.runtime.call("oauth.cb.onManualCodeInput", {});
    if (result?.cancel) throw new OAuthBridgeCancelled();
    return result?.value ?? "";
  }
}

export class ModelEventStream {
  constructor() {
    this.queue = [];
    this.queueHead = 0;
    this.waiters = [];
    this.terminal = false;
    this.resultPromise = new Promise((resolve) => { this.resolveResult = resolve; });
  }
  push(event) {
    if (this.terminal) return;
    if (event?.type === "done" || event?.type === "error") {
      this.terminal = true;
      this.resolveResult(event.type === "done" ? event.message : event.error);
    }
    const waiter = this.waiters.shift();
    if (waiter) waiter({ value: event, done: false });
    else this.queue.push(event);
    if (this.terminal) {
      for (const pending of this.waiters.splice(0)) pending({ value: undefined, done: true });
    }
  }
  fail(error, model) {
    const message = {
      role: "assistant", content: [], api: model?.api || "", provider: providerIDFromModel(model),
      model: modelIDFromModel(model), usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, totalTokens: 0, cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 } },
      stopReason: "error", errorMessage: error instanceof Error ? error.message : String(error), timestamp: Date.now(),
    };
    this.push({ type: "error", reason: "error", error: message });
  }
  result() { return this.resultPromise; }
  [Symbol.asyncIterator]() {
    return {
      next: () => {
        if (this.queueHead < this.queue.length) {
          const value = this.queue[this.queueHead];
          this.queue[this.queueHead++] = undefined;
          if (this.queueHead === this.queue.length) {
            this.queue = [];
            this.queueHead = 0;
          } else if (this.queueHead >= 1024 && this.queueHead * 2 >= this.queue.length) {
            this.queue = this.queue.slice(this.queueHead);
            this.queueHead = 0;
          }
          return Promise.resolve({ value, done: false });
        }
        if (this.terminal) return Promise.resolve({ value: undefined, done: true });
        return new Promise((resolve) => this.waiters.push(resolve));
      },
    };
  }
}

// Pi resolves terminal capabilities once per process and its extensions share that cache (terminal-image.ts getCapabilities). The host owns the terminal and has resolved them, so seed pi-tui's cache before Pi's theme reads it instead of probing the terminal again (under tmux, a synchronous subprocess). The seed belongs to PiG, not to the extension's environment.
function seedHostTerminalCapabilities() {
  const encoded = process.env.PIG_TERMINAL_CAPABILITIES;
  delete process.env.PIG_TERMINAL_CAPABILITIES;
  if (encoded) applyTerminalCapabilities(JSON.parse(encoded));
}

function applyTerminalCapabilities(caps) {
  if (!caps || typeof caps !== "object") return;
  setTerminalCapabilities({ images: caps.images || null, trueColor: caps.trueColor === true, hyperlinks: caps.hyperlinks === true });
}

export class Runtime {
  constructor(entry, nativeProviderObjects = new Map(), providerConfigObjects = new Map(), { name, socket } = {}) {
    seedHostTerminalCapabilities();
    // Pi initializes the process theme before loading extension factories.
    if (!globalThis[Symbol.for("@earendil-works/pi-coding-agent:theme")]) initTheme();
    this.entry = entry;
    this.name = name || process.env.PIG_EXT_NAME || basename(entry).replace(/\.[^.]+$/, "");
    this.socketPath = socket || process.env.PIG_EXT_SOCKET;
    this.version = "0.0.0";
    // Pi's per-extension API state (loader.ts createExtensionAPI): event-bus
    // subscriptions made while the factory runs are dropped if it throws.
    this.loadState = "loading";
    this.loadingUnsubscribers = [];
    this.extensionRuntime = createExtensionRuntime();
    this.hostReady = false;
    this.markdownTransformer = undefined;
    this.requestContext = new AsyncLocalStorage();
    this.activeRequests = new Map();
    // requestRecords holds each outstanding host request's ownership record.
    this.requestRecords = new Map();
    // An OAuth refresh signal the extension may keep after its request settles: the request's caller can still abort it. The entry holds the signal weakly and reaches the controller through the signal, so it lives exactly as long as the extension's reference.
    this.retainedSignals = new Map();
    this.signalControllers = new WeakMap();
    this.signalRegistry = new FinalizationRegistry(id => this.releaseRetainedSignal(id));
    this.requestTasks = new Set();
    this.providerTransport = new AbortController();
    this.handlers = new Map();
    this.nextHandlerId = 1;
    this.tools = new Map();
    // Raw terminal-input handlers, consulted synchronously while the host
    // waits for a consume decision. Empty means the host never round-trips.
    this.terminalInputHandlers = [];
    this.widthChangeHandlers = [];
    this.commands = new Map();
    this.shortcuts = new Map();
    this.flags = new Map();
    this.flagRegistrations = [];
    this.flagValues = new Map();
    this.providers = new Map();
    this.oauthProviders = new Map();
    this.providerStreams = new Map();
    // registeredProviderConfigs holds the provider configs this extension
    // registered, merged per upstream registerProvider, and nativeProviders
    // the provider objects it registered (upstream registerNativeProvider).
    this.registeredProviderConfigs = new Map();
    this.nativeProviders = new Map();
    this.nativeProviderKeys = new WeakMap();
    this.nativeProviderCallbacks = new Map();
    this.providerUpdates = new Map();
    this.providerObjectCallbacks = new Map();
    this.remoteProviderObjects = new Map();
    this.providerLeases = new FinalizationRegistry(reference => {
      if (!this.conn || this.conn.closed) return;
      this.conn.send({ type: "call", call: { method: "provider.release", args: reference } });
    });
    // pig additive (D19): members of one Node cell retain native Provider identity without serializing callbacks.
    this.nativeProviderObjects = nativeProviderObjects;
    // Pi keeps one effective registered configuration root per provider (model-runtime.ts:438-443,753-766). Members of one Node cell share that root and its original children, functions and descriptors.
    this.providerConfigObjects = providerConfigObjects;
    // Entries ctx.sessionManager.appendCustomEntry appended that the host's
    // replicated log does not hold yet.
    this.pendingEntries = [];
    // After the register frame the host has this extension's providers, and
    // a registration applies immediately, as after upstream's bindCore.
    this.providersSent = false;
    // The host registry snapshot behind ctx.modelRegistry's reads.
    this.registryState = { providers: {}, registered: [] };
    this.registryProviders = new Map();
    this.renderers = new Map();
    this.entryRenderers = new Map();
    // Upstream ToolExecutionComponent keeps one renderer state per tool card,
    // shared by renderCall and renderResult, and each renderer's last
    // component. The host names the card and releases it when the card is gone.
    this.toolRenderCards = new Map();
    this.modelStreams = new Map();
    this.modelStreamCallbacks = new Map();
    this.models = new Map();
    this.nextModelStreamId = 1;
    this.footerFactory = undefined;
    this.headerFactory = undefined;
    this.widgets = new Map();
    this.customOverlays = new Map();
    this.branchChangeCallbacks = new Set();
    this.specialSurfaceComponents = new Map();
    this.customOverlaySeq = 0;
    this.ready = { cwd: process.cwd(), width: 80, model: "" };
    this.sessionLogRequested = false;
    this.sessionLogGeneration = 0;
    this.sessionLogSync = undefined;
    this.state = {
      activeTools: [],
      allTools: [],
      commands: [],
      thinkingLevel: "",
      model: undefined,
      session: { sessionId: "", sessionName: "", sessionFile: "", leafId: "", entries: [] },
      isIdle: true,
      projectTrusted: true,
      hasPendingMessages: false,
      editorText: "",
      toolsExpanded: false,
      allThemes: [],
      contextUsage: undefined,
      systemPrompt: "",
      systemPromptOptions: {},
      flags: {},
      hasUI: false,
      footerData: { gitBranch: null, extensionStatuses: {}, availableProviderCount: 0 },
    };
    this.autocomplete = new AutocompleteRuntime(this);
    this.ui = new RuntimeUI(this);
    // 0.3.0: replaced by Pi runner wiring
    this.editorHost = new EditorComponentHost(this);
    this.editorFactory = undefined;
    this.keybindingTable = undefined;
    this.noOpUI = createNoOpUI(this.ui);
    this.ctx = new RuntimeContext(this);
    this.api = this.buildAPI();
    setRuntime(this);
  }

  // Ports packages/coding-agent/src/core/extensions/loader.ts (factory API guards).
  buildAPI() {
    // Upstream createExtensionRuntime: action methods throw while the factory
    // runs, before the runtime is bound to the host.
    const action = (fn) => (...args) => {
      if (!this.conn || !this.hostReady) throw new Error("Extension runtime not initialized. Action methods cannot be called during extension loading.");
      return fn(...args);
    };
    const active = (fn) => (...args) => {
      // upstream: packages/coding-agent/src/core/extensions/loader.ts:createExtensionAPI
      if (this.loadState === "failed") throw new Error(`Extension "${this.entry}" failed to load and its API is no longer active.`);
      this.extensionRuntime.assertActive();
      return fn(...args);
    };
    const api = {
      on: (event, handler) => this.on(event, handler),
      registerTool: (definition) => this.registerTool(definition),
      registerCommand: (name, definition) => this.registerCommand(name, definition),
      registerShortcut: (key, definition) => this.registerShortcut(key, definition),
      registerFlag: (name, definition) => this.registerFlag(name, definition),
      registerMessageRenderer: (customType, handler) => this.registerMessageRenderer(customType, handler),
      registerEntryRenderer: (customType, handler) => this.registerEntryRenderer(customType, handler),
      registerMarkdownTransformer: (transformer) => {
        this.markdownTransformer = transformer;
      },
      registerProvider: (name, config) => this.registerProvider(name, config),
      unregisterProvider: (name) => this.unregisterProvider(name),
      sendMessage: action((message, options = {}) => this.fireAndForget("sendMessage", { message, options })),
      sendUserMessage: action((content, options = {}) => this.fireAndForget("sendUserMessage", { content, options })),
      appendEntry: action((customType, data) => this.fireAndForget("appendEntry", { customType, data })),
      messageRole: (data) => Runtime.messageRole(data),
      messageText: (data) => Runtime.messageText(data),
      getSessionName: action(() => this.state.session?.sessionName || undefined),
      setSessionName: action((name) => this.fireAndForget("setSessionName", { name })),
      setLabel: action((entryId, label) => this.fireAndForget("setLabel", { entryId, label })),
      exec: (command, args = [], options = undefined) => this.call("exec", { command, args, options }),
      getFlag: (name) => {
        if (!this.flags.has(name)) return undefined;
        return (Object.hasOwn(this.state.flags, name) ? this.state.flags[name] : undefined) ?? this.flagValues.get(name);
      },
      getActiveTools: action(() => this.callSync("getActiveTools")?.tools || []),
      // Tool registries can change in another extension while this handler is running.
      // The host call also carries PiG's legacy per-tool "source" for the Go,
      // Rust and Python SDKs; Pi's ToolInfo has only sourceInfo, so drop it.
      getAllTools: action(() => (this.callSync("getAllTools")?.tools || []).map(({ source: _legacySource, ...tool }) => tool)),
      getCommands: action(() => structuredClone(this.state.commands || [])),
      getThinkingLevel: action(() => this.state.thinkingLevel || ""),
      setThinkingLevel: action((level) => {
        this.state.thinkingLevel = level;
        this.fireAndForget("setThinkingLevel", { level });
      }),
      setActiveTools: action((tools) => {
        this.state.activeTools = [...tools];
        this.fireAndForget("setActiveTools", { tools });
      }),
      setModel: async (model) => {
        if (!this.conn || !this.hostReady) throw new Error("Extension runtime not initialized");
        const result = await this.call("setModel", { model: `${model.provider}/${model.id}` });
        if (result?.error) throw new Error(result.error);
        return result?.success === true;
      },
      // loader.ts createExtensionAPI events: the shared bus, with on()
      // returning the unsubscribe function.
      events: {
        emit: active((channel, data) => {
          // pig divergence (D83): listeners in other realms reach the original payload through xref proxies; independent tasks across realms are ordered by timing.
          sharedBus.emit(this, channel, data);
        }),
        on: active((channel, handler) => {
          const unsubscribe = this.extensionRuntime.trackEventBusSubscription(sharedBus.on(this, channel, handler));
          if (this.loadState === "loading") this.loadingUnsubscribers.push(unsubscribe);
          return unsubscribe;
        }),
      },
    };
    // Guard before invoking even async methods so a failed API throws synchronously, as Pi does.
    for (const [name, method] of Object.entries(api)) {
      if (typeof method === "function" && name !== "messageRole" && name !== "messageText") api[name] = active(method);
    }
    return api;
  }

  // commitLoad and discardLoad end the factory's loading state as Pi's
  // loader does on success and on a throw.
  commitLoad() {
    if (this.loadState !== "loading") return;
    this.loadState = "active";
    this.loadingUnsubscribers = [];
    for (const [id, config] of this.registeredProviderConfigs) this.transferProviderOwnership(id, config);
    for (const provider of this.nativeProviders.values()) {
      this.transferProviderOwnership(provider.id, undefined);
      this.nativeProviderObjects.set(provider.id, { owner: this, provider });
    }
  }

  // registeredProviderConfig returns the effective configuration root this process retains for a provider: the cell's shared root once this member is active, else its own loading-time root.
  registeredProviderConfig(name) {
    return this.loadState === "active" ? this.providerConfigObjects.get(name)?.config : this.registeredProviderConfigs.get(name);
  }

  // A registration or unregister by another member of the cell replaces this member's local ownership of the provider without a host call.
  forgetProviderOwnership(name) {
    this.registryProviders.delete(name);
    this.providers.delete(name);
    this.providerStreams.delete(name);
    this.oauthProviders.delete(name);
    this.registeredProviderConfigs.delete(name);
    this.nativeProviders.delete(name);
  }

  // providerConfigRef encodes an effective root for the Host, which lends it to readers in other processes (model-runtime.ts:438-443).
  providerConfigRef(root) {
    this.ensureXrefHello();
    return xrefRealm.encode(root);
  }

  // Registrations made while loading reach the Host in the handshake; their roots follow once it is complete.
  publishProviderConfigRefs() {
    if (this.providerConfigRefsPublished) return;
    this.providerConfigRefsPublished = true;
    for (const [name, root] of this.registeredProviderConfigs) {
      if (this.providers.has(name)) this.fireAndForget("provider.configRef", { name, configRef: this.providerConfigRef(root) });
    }
  }

  // A registration in this process merges over another process's effective root only through its xref; a native SDK author's snapshot is not merged.
  foreignProviderConfigForMerge(name) {
    return this.registryState.registered?.some((entry) => entry.name === name && entry.config) ? this.foreignProviderConfig(name) : undefined;
  }

  // foreignProviderConfig returns another process's effective root through its xref, or undefined when the Host holds none for the provider.
  foreignProviderConfig(name) {
    if (!this.connection()) return undefined;
    const result = this.xrefCall("provider.config", { name });
    return result?.ref ? xrefRealm.decode(result.ref) : undefined;
  }

  // transferProviderOwnership records this member as the cell's owner of a provider root.
  transferProviderOwnership(name, config) {
    const previous = this.providerConfigObjects.get(name)?.owner ?? this.nativeProviderObjects.get(name)?.owner;
    if (previous && previous !== this) previous.forgetProviderOwnership(name);
    this.nativeProviderObjects.delete(name);
    if (config) this.providerConfigObjects.set(name, { owner: this, config });
    else this.providerConfigObjects.delete(name);
  }

  discardLoad() {
    if (this.loadState !== "loading") return;
    this.loadState = "failed";
    for (const unsubscribe of this.loadingUnsubscribers.splice(0)) unsubscribe();
  }

  // Retain callable identities for host-owned dispatch snapshots; removal changes the host's next snapshot, not an already admitted invocation.
  on(event, handler) {
    const bucket = this.handlers.get(event) ?? [];
    const entry = { id: this.nextHandlerId++, handler, removed: false };
    bucket.push(entry);
    this.handlers.set(event, bucket);
    if (this.conn) this.fireAndForget("event.subscribe", { event, handlerId: entry.id });
    return () => {
      if (entry.removed) return;
      entry.removed = true;
      if (this.conn) this.fireAndForget("event.unsubscribe", { event, handlerId: entry.id });
    };
  }

  registerTool(definition) {
    if (!definition || !definition.name) throw new Error("registerTool requires a definition with name");
    if (typeof definition.parameters !== "object" || definition.parameters === null || Array.isArray(definition.parameters)) {
      throw new Error(`Tool "${definition.name}" registered by extension "${this.entry}" must define an object parameter schema.`);
    }
    this.tools.set(definition.name, definition);
    if (this.conn) this.applyState(this.callSync("registerTool", this.toolDeclaration(definition)));
  }

  toolDeclaration(tool) {
    return {
      name: tool.name,
      label: tool.label,
      description: tool.description || "",
      parameters: tool.parameters,
      // TypeBox kinds are non-enumerable and remain host-only validation metadata.
      validation_parameters: JSON.parse(JSON.stringify(tool.parameters || {}, (_key, value) => {
        if (value && typeof value === "object" && !Array.isArray(value) && value["~kind"]) {
          const copy = { ...value, "~kind": value["~kind"] };
          if (["Intersect", "Enum", "TemplateLiteral"].includes(value["~kind"])) {
            const { Evaluate, Instantiate } = requireModule("./shims/typebox.mjs");
            copy["~convert"] = Evaluate(value["~kind"] === "Intersect" ? Instantiate({}, value) : value);
          }
          if (value === tool.parameters && Object.getOwnPropertySymbols(value).includes(Symbol.for("TypeBox.Kind"))) copy["~skipCoercion"] = true;
          return copy;
        }
        if (value === tool.parameters && Object.getOwnPropertySymbols(value).includes(Symbol.for("TypeBox.Kind"))) return { ...value, "~skipCoercion": true };
        return value;
      })),
      constrained_sampling: tool.constrainedSampling,
      execution_mode: tool.executionMode,
      prompt_snippet: tool.promptSnippet,
      prompt_guidelines: tool.promptGuidelines,
      annotations: tool.annotations,
      source: tool.source,
      render_shell: tool.renderShell === "self" ? "self" : undefined,
      renders_call: (typeof tool.renderCall === "function" && !tool.renderCall[builtInToolRenderer]) || undefined,
      renders_result: (typeof tool.renderResult === "function" && !tool.renderResult[builtInToolRenderer]) || undefined,
      builtin_renderers: tool.renderCall?.[builtInToolRenderer] ?? tool.renderResult?.[builtInToolRenderer],
    };
  }

  registerCommand(name, definition) {
    if (typeof name === "object" && name) {
      definition = name;
      name = definition.name;
    }
    this.commands.set(name, { name, ...definition });
  }

  registerShortcut(key, definition) {
    this.shortcuts.set(key, { key, ...definition });
  }

  registerFlag(name, definition) {
    if (definition.default !== undefined && typeof definition.default !== definition.type) {
      throw new Error(`Invalid default for flag "${name}": expected ${definition.type}, got ${typeof definition.default}`);
    }
    const flag = { name, ...definition };
    this.flags.set(name, flag);
    this.flagRegistrations.push(flag);
    if (definition.default !== undefined && !this.flagValues.has(name)) {
      this.flagValues.set(name, definition.default);
    }
  }

  registerMessageRenderer(customType, handler) {
    this.renderers.set(customType, handler);
  }

  registerEntryRenderer(customType, handler) {
    this.entryRenderers.set(customType, handler);
  }

  // A factory that uses the shared bus while loading needs its connection before it registers. The IO worker's readiness is awaited synchronously, as any synchronous host call waits.
  // The registered connection, or the connection a loading factory opened for the shared bus.
  connection() {
    return this.conn ?? this.earlyConn;
  }

  ensureConnectionSync() {
    if (this.connection()) return;
    const sockPath = this.socketPath;
    if (!sockPath) throw new Error("PIG_EXT_SOCKET not set");
    const socket = new ProviderSocket(sockPath);
    let failure;
    socket.once("error", error => { failure ??= error; });
    socket.waitUntil(() => socket.highWaterMark !== undefined || failure !== undefined);
    if (failure) throw failure;
    this.earlyConn = this.attachConnection(socket);
  }

  ensureXrefHello() {
    this.ensureConnectionSync();
    const connection = this.connection();
    if (connection.xrefRealm) return;
    connection.callSync("xref.hello", { realm: xrefRealm.id }, "");
    connection.xrefRealm = xrefRealm.id;
  }

  xrefCall(method, args) {
    this.ensureXrefHello();
    // Before registration no host request is active, so the call has no parent.
    if (!this.conn) return this.earlyConn.callSync(method, args, "");
    return this.callSync(method, args);
  }

  busCall(method, args) {
    return this.xrefCall(method, args);
  }

  // Serves one restricted xref/bus request in its own request scope, so host calls it makes use a fresh ordering lane and end with it.
  serveXref(conn, env) {
    const request = { id: env.id, controller: new AbortController(), connection: conn, pendingHostCalls: new Set(), settled: false, responded: false, cancelled: false };
    let result = null;
    let failure = null;
    let emission;
    try {
      conn.requestState(env.id, "started");
      runWithRuntime(this, () => this.requestContext.run(request, () => {
        try {
          const args = env.request.args ?? {};
          if (env.request.method === "xref.op") result = xrefRealm.serve(args);
          else if (env.request.method === "events.dispatch") {
            sharedBus.dispatch(args);
            emission = args.emission;
          }
          else result = { realm: xrefRealm.id, listeners: sharedBus.migrate().map(({ channel, handlerId, runtime }) => ({ channel, handlerId, extension: runtime.name })) };
        } catch (error) {
          failure = error;
        }
      }));
    } finally {
      request.settled = true;
    }
    // The emitter's settle waits until the microtasks this prefix queued have run. A prefix reached while this process already waits in a synchronous call cannot run them first, so it reports at once rather than wait on its own waiter.
    // pig divergence (D83): only the first post-await generation is ordered with the emitter's microtasks.
    const quiesce = () => { if (!conn.closed) conn.notify("events.quiesce", { emission }); };
    const waitForEmitter = emission !== undefined && failure === null && !isWaiting();
    if (waitForEmitter) lockstep.add(emission);
    try { conn.respond(env.id, result, failure); }
    catch (error) { try { conn.respond(env.id, null, error); } catch {} }
    if (emission === undefined || failure !== null) return;
    if (!waitForEmitter) {
      quiesce();
      return;
    }
    try { conn.socket.waitUntil(() => !lockstep.has(emission) || conn.closed); }
    catch {}
    finally { lockstep.delete(emission); }
    setImmediate(quiesce);
  }

  async reportLoadFailureOnConnection(failure) {
    const conn = this.earlyConn;
    if (conn.closed) return;
    const closed = new Promise(resolve => conn.socket.once("close", resolve));
    conn.notify("load_failed", failure);
    await closed;
    quitDrain.runtimes.delete(this);
    sharedBus.disposeRuntime(this);
    liveRuntimes.delete(this);
  }

  attachConnection(socket) {
    const conn = new Connection(socket);
    liveRuntimes.add(this);
    this.xrefTransport = {
      call: (method, args) => this.xrefCall(method, args),
      notify: (method, args) => {
        this.ensureXrefHello();
        this.connection().notify(method, args);
      },
    };
    conn.xrefHandler = env => this.serveXref(conn, env);
    quitDrain.runtimes.add(this);
    conn.syncHandler = request => request.method === "facet_sync" ? dispatchFacetSync(this, request) : request.method === "autocomplete.sync" ? this.autocomplete.sync(request.args) : dispatchProviderObjectSync(this, request);
    this.removeDrainListener = conn.onDrain(() => {
      for (const [key, widget] of this.widgets) {
        if (widget.renderRequested) this.requestWidgetRender(key);
      }
      for (const overlay of this.customOverlays.values()) {
        if (overlay.renderRequested) overlay.renderFrame();
      }
    });
    return conn;
  }

  async connect() {
    if (!this.conn) {
      if (this.earlyConn) this.conn = this.earlyConn;
      else {
        const sockPath = this.socketPath;
        if (!sockPath) throw new Error("PIG_EXT_SOCKET not set");
        this.conn = this.attachConnection(await ProviderSocket.connect(sockPath));
      }
      this.earlyConn = undefined;
    }
    this.conn.send({
      type: "register",
      register: {
        name: this.name,
        version: this.version,
        tools: [...this.tools.values()].map((tool) => this.toolDeclaration(tool)),
        commands: [...this.commands.values()].map((cmd) => ({
          name: cmd.name,
          description: cmd.description || "",
          args: cmd.args,
          argument_completions: typeof cmd.getArgumentCompletions === "function" || undefined,
        })),
        shortcuts: [...this.shortcuts.values()].map((s) => ({ key: s.key, description: s.description || "" })),
        handlers: [...this.handlers.entries()].flatMap(([event, handlers]) =>
          handlers.filter(({ removed }) => !removed).map(({ id }) => ({ event, can_block: BLOCKING_EVENTS.has(event), handler_id: id })),
        ),
        flags: this.flagRegistrations.map((f) => ({ name: f.name, description: f.description || "", type: f.type || "string", default: f.default })),
        providers: [...this.providers.entries()].map(([name, config]) => ({ name, config, ...(this.providerStreams.has(name) ? {stream_simple:true} : {}) })).concat([...this.nativeProviders.values()].map(provider=>({name:provider.id,config:{},native:nativeDeclaration(provider, this.nativeProviderKeys.get(provider))}))),
        message_renderers: [...this.renderers.keys()].map((customType) => ({ custom_type: customType })),
        entry_renderers: [...this.entryRenderers.keys()].map((customType) => ({ custom_type: customType })),
        markdown_transformer: typeof this.markdownTransformer === "function" || undefined,
        // The host sends no session log until an extension asks for it, so the
        // majority which never inspect the session do not each hold a full copy
        // of it resident. The Go, Rust and Python SDKs ask on the first read,
        // which they can do because a read there may block on the host. This
        // runtime exposes getEntries/getBranch synchronously over asynchronous
        // IPC and so has no such moment; it asks here instead, and the host
        // enrols it before building the ready state. Same interface, same
        // answers, different means.
        wants_session_log: true,
      },
    });
    this.providersSent = true;
  }

  // The host retires a generation by waiting for its connection to close after teardown, because the process outlives the generation.
  async run() {
    try {
      await this.serve();
    } finally {
      this.conn?.socket?.destroy?.();
    }
  }

  async serve() {
    try {
      await this.connect();
      while (true) {
        const env = await this.conn.next();
        if (!env) return;
        if (env.type === "call_result") {
          // Apply earlier state and stream notifications before an awaited call resumes its handler.
          this.conn.resolveCall(env);
          continue;
        }
        if (env.type === "ready") {
          this.publishProviderConfigRefs();
          this.hostReady = true;
          this.ready = env.ready || this.ready;
          syncTerminalGeometry(this.ready);
          this.replaceModels(this.ready.models);
          this.ctx.cwd = this.ready.cwd || this.ctx.cwd;
          this.ctx.mode = this.ready.mode || this.ctx.mode;
          this.ctx.model = this.ready.model ? { id: this.ready.model } : this.ctx.model;
          if (env.ready?.state) this.applyState(env.ready.state);
          // Pi's interactive mode loads every highlight.js language at
          // startup (interactive-mode.ts); print, JSON and RPC mode keep the
          // eager set.
          if (this.ctx.mode === "tui") void loadAllHighlightLanguages();
          // The host owns process teardown, so flush bytecode when processing ready rather than relying on normal Node exit. Factory/module state is not persisted.
          flushCompileCache();
          continue;
        }
        if (env.type === "ping") {
          this.conn.send({ type: "pong", pong: { nonce: env.ping?.nonce || "" } });
          continue;
        }
        if (env.type === "notify" && env.notify) {
          this.handleNotify(env.notify);
          continue;
        }
        if (env.type === "shutdown") return;
        if (env.type === "cancel") {
          const requestId = env.cancel?.request_id || env.id || "";
          (this.activeRequests.get(requestId) ?? this.retainedController(requestId))?.abort(env.cancel?.reason || "cancelled");
          this.conn.cancelParent(requestId);
          continue;
        }
        if (env.type === "request") {
          this.conn.requestState(env.id, "started");
          const controller = new AbortController();
          this.activeRequests.set(env.id, controller);
          // Pi's context getters are enumerable own properties. Forward reads to the live context instead of snapshotting values or hiding them on a prototype; extensions can spread a context and retain ui/session/model access.
          const requestCtx = Object.create(Object.getPrototypeOf(this.ctx));
          for (const key of Object.keys(this.ctx)) {
            if (key === "signal") continue;
            const value = this.ctx[key];
            const descriptor = typeof value === "function" ? { value, writable: true } : { get: () => this.ctx[key] };
            Object.defineProperty(requestCtx, key, { enumerable: true, configurable: true, ...descriptor });
          }
          Object.defineProperty(requestCtx, "signal", { enumerable: true, configurable: true, get: () => controller.signal });
          // Dispatch concurrently so long-running interactive calls
          // (e.g. ui.custom blocking on done()) don't starve incoming
          // notifications such as ui.custom.input. The handler will
          // serialize its response back through the connection on its
          // own when it resolves.
          const quitting = env.request?.method === "event" && env.request.event === "session_shutdown" && env.request.args?.reason === "quit";
          const quitTail = quitDrain.active && env.request?.method === "event" && ["turn_end", "agent_settled"].includes(env.request.event);
          const request = { id: env.id, controller, connection: this.conn, pendingHostCalls: new Set(), settled: false, responded: false, cancelled: false, quit: quitting, quitTail };
          this.requestRecords.set(env.id, request);
          if (quitting) enterQuitDrain();
          refreshQuitDrain();
          const task = this.requestContext.run(request, async () => {
            try {
              await this.handleRequest(env.id, env.request || {}, requestCtx);
            } finally {
              request.cancelled ||= !request.settled && controller.signal.aborted;
              request.settled = true;
              this.activeRequests.delete(env.id);
              this.requestRecords.delete(env.id);
              refreshQuitDrain();
            }
          });
          this.requestTasks.add(task);
          void task.then(
            () => this.requestTasks.delete(task),
            () => this.requestTasks.delete(task),
          );
        }
      }
    } finally {
      quitDrain.runtimes.delete(this);
      // The connection is gone, so its shared-bus listeners leave the Host registry before invalidation unsubscribes them.
      sharedBus.disposeRuntime(this);
      this.extensionRuntime.invalidate();
      this.hostReady = false;
      liveRuntimes.delete(this);
      this.providerTransport.abort(new Error("Provider connection closed"));
      this.autocomplete.dispose();
      for (const [id, entry] of this.nativeProviderObjects) {
        if (entry.owner === this) this.nativeProviderObjects.delete(id);
      }
      for (const [id, entry] of this.providerConfigObjects) {
        if (entry.owner === this) this.providerConfigObjects.delete(id);
      }
      this.nativeProviders.clear();
      this.nativeProviderCallbacks.clear();
      this.remoteProviderObjects.clear();
      this.providerObjectCallbacks.clear();
      this.providerUpdates.clear();
      this.removeDrainListener?.();
      for (const [requestId, controller] of this.activeRequests) {
        controller.abort("shutdown");
        this.conn.cancelParent(requestId);
      }
      for (const overlay of this.customOverlays.values()) {
        overlay.active = false;
        if (overlay.renderTimer !== undefined) clearTimeout(overlay.renderTimer);
        try { overlay.dispose?.(); } catch {}
      }
      this.customOverlays.clear();
      for (const widget of this.widgets.values()) {
        widget.active = false;
        if (widget.renderTimer !== undefined) clearTimeout(widget.renderTimer);
        try { widget.component?.dispose?.(); } catch {}
      }
      this.widgets.clear();
      // No dispatcher remains to apply model notifications after this point.
      // End streams before joining child agents so their abort can settle.
      for (const [streamId, stream] of this.modelStreams) {
        stream.fail(new Error("Extension connection closed"), this.modelStreamCallbacks.get(streamId)?.model);
      }
      await Promise.all([...this.modelStreamCallbacks.values()].map(callbacks => callbacks.disposeFetch?.()));
      await disposeIndependentSessions(this);
      if (this.requestTasks.size > 0) {
        let timer;
        const drained = await Promise.race([
          Promise.allSettled([...this.requestTasks]).then(() => true),
          new Promise((resolve) => { timer = setTimeout(() => resolve(false), 1000); }),
        ]);
        if (timer !== undefined) clearTimeout(timer);
        if (!drained) throw new Error("extension handlers did not stop before the shutdown deadline");
      }
    }
  }

  clearWidget(key) {
    const previous = this.widgets.get(key);
    if (previous) {
      previous.active = false;
      if (previous.renderTimer !== undefined) clearTimeout(previous.renderTimer);
      try { previous.component?.dispose?.(); } catch {}
      this.widgets.delete(key);
    }
    this.fireAndForget("ui.setWidget", { key, content: null });
  }

  setWidgetFactory(key, factory, options = {}) {
    const previous = this.widgets.get(key);
    if (previous) {
      previous.active = false;
      if (previous.renderTimer !== undefined) clearTimeout(previous.renderTimer);
      try { previous.component?.dispose?.(); } catch {}
    }
    let requestFrame = () => {};
    const tuiShim = {
      requestRender: () => requestFrame(),
      get width() { return Number(thisRuntime.ready?.width || 80); },
      get height() { return Number(thisRuntime.ready?.height || 24); },
    };
    const thisRuntime = this;
    const component = factory(tuiShim, this.ui.theme);
    if (!component || typeof component.render !== "function") {
      throw new Error("setWidget factory must return a renderable component");
    }
    const widget = {
      component,
      options,
      lastLines: [],
      active: true,
      renderRequested: false,
      renderTimer: undefined,
      lastRenderAt: Number.NEGATIVE_INFINITY,
    };
    this.widgets.set(key, widget);
    requestFrame = () => this.requestWidgetRender(key);
    widget.lastRenderAt = performance.now();
    this.renderWidget(key);
  }

  requestWidgetRender(key) {
    const widget = this.widgets.get(key);
    if (!widget?.active) return;
    widget.renderRequested = true;
    if (widget.renderTimer !== undefined || this.conn?.socket?.writableNeedDrain) return;
    const elapsed = performance.now() - widget.lastRenderAt;
    const delay = Math.max(0, REMOTE_RENDER_INTERVAL_MS - elapsed);
    widget.renderTimer = setTimeout(() => {
      widget.renderTimer = undefined;
      if (!widget.active || !widget.renderRequested) return;
      if (this.conn?.socket?.writableNeedDrain) return;
      widget.renderRequested = false;
      widget.lastRenderAt = performance.now();
      this.renderWidget(key);
      if (widget.renderRequested) this.requestWidgetRender(key);
    }, delay);
  }

  renderWidget(key) {
    const widget = this.widgets.get(key);
    if (!widget?.active) return;
    if (this.conn?.socket?.writableNeedDrain) {
      widget.renderRequested = true;
      return;
    }
    const width = Number(this.ready?.width || 80);
    const rendered = widget.component.render(width);
    const lines = Array.isArray(rendered) ? rendered.map((line) => String(line)) : [];
    if (width === widget.lastWidth && lines.length === widget.lastLines.length && lines.every((line, index) => line === widget.lastLines[index])) return;
    widget.lastLines = lines;
    widget.lastWidth = width;
    if (widget.options?.placement) {
      this.fireAndForget("ui.setWidget", { key, content: lines, options: widget.options, width });
    } else {
      this.conn?.pushWidget(key, lines, width);
    }
  }

  applyState(snapshot) {
    if (!snapshot || typeof snapshot !== "object") return;
    for (const key of ["activeTools", "allTools", "commands", "allThemes"]) {
      if (Object.hasOwn(snapshot, key)) this.state[key] = [...(snapshot[key] ?? [])];
    }
    if (typeof snapshot.thinkingLevel === "string") this.state.thinkingLevel = snapshot.thinkingLevel;
    if (Array.isArray(snapshot.scopedModels)) this.state.scopedModels = snapshot.scopedModels;
    if (Object.hasOwn(snapshot, "model")) this.state.model = normalizeModel(snapshot.model);
    if (snapshot.session && typeof snapshot.session === "object") {
      // 0.3.0: replaced by Pi runner wiring
      // A cursor belongs to one session. Even an empty replacement must drop
      // the outgoing log before folding pages from the destination.
      if (snapshot.session.sessionId && snapshot.session.sessionId !== this.state.session.sessionId) {
        this.pendingEntries = [];
        this.ctx.sessionManager._cachedIndex = undefined;
        this.state.session = { entries: [] };
        this.sessionLogRequested = false;
        this.sessionLogGeneration++;
      }
      const persistedSession = (snapshot.session.sessionFile ?? this.state.session.sessionFile) !== "";
      if ((Array.isArray(snapshot.session.entriesAppended) && snapshot.session.entriesAppended.length > 0) ||
          (!persistedSession && typeof snapshot.session.entryCount === "number")) {
        this.sessionLogRequested = true;
      }
      this.state.session = {
        ...this.state.session,
        sessionId: snapshot.session.sessionId ?? this.state.session.sessionId,
        sessionName: snapshot.session.sessionName ?? this.state.session.sessionName,
        sessionFile: snapshot.session.sessionFile ?? this.state.session.sessionFile,
        leafId: snapshot.session.leafId ?? this.state.session.leafId,
        info: snapshot.session.info ?? this.state.session.info,
        entries: this.applyEntryAppend(snapshot.session),
      };
      this.branchCache = undefined;
    }
    if (typeof snapshot.isIdle === "boolean") this.state.isIdle = snapshot.isIdle;
    if (typeof snapshot.projectTrusted === "boolean") this.state.projectTrusted = snapshot.projectTrusted;
    if (snapshot.footerData && typeof snapshot.footerData === "object") {
      const previousBranch = this.state.footerData.gitBranch;
      this.state.footerData = {
        gitBranch: snapshot.footerData.gitBranch || null,
        extensionStatuses: snapshot.footerData.extensionStatuses ?? {},
        availableProviderCount: snapshot.footerData.availableProviderCount ?? 0,
      };
      if (this.state.footerData.gitBranch !== previousBranch) this.notifyBranchChange();
    }
    if (typeof snapshot.hasPendingMessages === "boolean") this.state.hasPendingMessages = snapshot.hasPendingMessages;
    if (typeof snapshot.editorText === "string") this.state.editorText = snapshot.editorText;
    if (typeof snapshot.toolsExpanded === "boolean") this.state.toolsExpanded = snapshot.toolsExpanded;
    if (Object.hasOwn(snapshot, "contextUsage")) {
      this.state.contextUsage = snapshot.contextUsage == null ? undefined : { ...snapshot.contextUsage };
    }
    if (snapshot.systemPromptOptions && typeof snapshot.systemPromptOptions === "object") {
      this.state.systemPromptOptions = snapshot.systemPromptOptions;
    }
    if (typeof snapshot.systemPrompt === "string") {
      this.state.systemPrompt = snapshot.systemPrompt;
    }
    if (Object.hasOwn(snapshot, "flags")) this.state.flags = { ...snapshot.flags };
    // Pi's TUI and its extensions share one capability cache; here the host
    // owns the terminal, so its resolved capabilities seed the cache pi-tui's
    // Markdown reads.
    // The host's active theme colors ctx.ui.theme and the pi-coding-agent
    // theme helpers, as Pi's global theme does in its own process.
    if (snapshot.theme && typeof snapshot.theme === "object") this.ui.theme.setPalette(snapshot.theme);
    applyTerminalCapabilities(snapshot.terminalCapabilities);
    if (snapshot.keybindings && typeof snapshot.keybindings === "object") this.applyKeybindings(snapshot.keybindings);
    if (typeof snapshot.hasUI === "boolean") this.state.hasUI = snapshot.hasUI;
    this.ctx.model = normalizeModel(this.state.model);
    this.renderSpecialSurface("header");
    this.renderSpecialSurface("footer");
  }

  // keybindings is Pi's keybindings manager, which Pi hands editor and
  // custom-component factories.
  keybindings() {
    return getKeybindings();
  }

  // applyKeybindings installs the host's keybinding table (every tui.* and
  // app.* definition with the user's overrides) as pi-tui's keybindings, as
  // Pi installs its manager with setKeybindings at startup.
  applyKeybindings(table) {
    if (!table || typeof table !== "object" || !table.definitions) return;
    const encoded = JSON.stringify(table);
    if (encoded === this.keybindingTable) return;
    this.keybindingTable = encoded;
    setKeybindings(new KeybindingsManager(table.definitions, table.userBindings || {}));
  }

  readSessionFile() {
    const path = this.state.session?.sessionFile;
    if (!path) return [];
    const entries = [];
    const decoder = new TextDecoder();
    const buffer = Buffer.allocUnsafe(1024 * 1024);
    let pending = "";
    let fd;
    try {
      fd = openSync(path, "r");
      for (;;) {
        const count = readSync(fd, buffer, 0, buffer.length, null);
        if (count === 0) break;
        pending += decoder.decode(buffer.subarray(0, count), { stream: true });
        if (Buffer.byteLength(pending, "utf-8") > MAX_FRAME_SIZE && !pending.includes("\n")) {
          throw new Error("session entry exceeds the extension frame limit");
        }
        let newline;
        while ((newline = pending.indexOf("\n")) !== -1) {
          const line = pending.slice(0, newline);
          pending = pending.slice(newline + 1);
          if (!line) continue;
          const entry = JSON.parse(line);
          if (entry?.type !== "session") entries.push(entry);
        }
      }
      pending += decoder.decode();
      if (pending.trim()) {
        const entry = JSON.parse(pending);
        if (entry?.type !== "session") entries.push(entry);
      }
      return entries;
    } catch {
      return [];
    } finally {
      if (fd !== undefined) closeSync(fd);
    }
  }

  ensureSessionLog() {
    if (this.sessionLogRequested) return;
    this.sessionLogRequested = true;
    const entries = this.readSessionFile();
    this.state.session.entries = entries;
    this.branchCache = undefined;
    const owner = this.activeRequest();
    let operation;
    operation = this.syncSessionLog(entries.length, this.sessionLogGeneration).catch((error) => {
      try { process.stderr.write(`pig: session synchronization failed: ${error?.message || String(error)}\n`); } catch {}
    }).finally(() => {
      owner?.pendingHostCalls.delete(operation);
    });
    owner?.pendingHostCalls.add(operation);
    this.sessionLogSync = operation;
  }

  async syncSessionLog(startCursor, generation = this.sessionLogGeneration) {
    let cursor = startCursor;
    let complete = false;
    for (;;) {
      const result = await this.call("watchSessionLog", { cursor, complete });
      if (generation !== this.sessionLogGeneration) return;
      const entries = Array.isArray(result?.entries) ? result.entries : [];
      const next = Number(result?.entryCount ?? cursor);
      this.applyState({ session: {
        leafId: result?.leafId ?? this.state.session.leafId,
        entriesAppended: entries,
        entryCount: next,
      }});
      cursor = next;
      if (!result?.hasMore) {
        if (complete && entries.length === 0) return;
        complete = true;
      }
    }
  }

  // applyEntryAppend folds a bounded ordered session page into the local log.
  // entryCount is the cursor after that page; a cursor mismatch means the
  // session changed and the local copy must be replaced.
  applyEntryAppend(session) {
    const local = this.state.session.entries || [];
    const appended = Array.isArray(session.entriesAppended) ? session.entriesAppended : [];
    const total = typeof session.entryCount === "number" ? session.entryCount : undefined;
    if (total === undefined) return local;
    // No entries and a zero count is the shape sent to an extension that has
    // not subscribed to the log. It says nothing about the log's contents, and
    // treating it as an empty session would discard what a concurrent
    // subscribe had just installed.
    if (total === 0 && appended.length === 0) return local;
    if (total < local.length || total === appended.length) return appended;
    if (appended.length === 0) return local;
    return local.concat(appended);
  }

  // branchFromEntries walks parent links back from the leaf, the same path
  // upstream derives in process. Cached until the next entry append or leaf
  // move so repeated reads within a turn stay O(1).
  branchFromEntries() {
    const session = this.state.session;
    const entries = session.entries || [];
    if (this.branchCache && this.branchCacheLeaf === session.leafId) return [...this.branchCache];
    const leafId = session.leafId || undefined;
    const byId = new Map();
    for (const entry of entries) {
      if (entry && entry.id !== undefined) byId.set(entry.id, entry);
    }
    const path = [];
    const seen = new Set();
    let current = leafId === undefined ? undefined : byId.get(leafId);
    while (current && !seen.has(current.id)) {
      seen.add(current.id);
      path.push(current);
      current = current.parentId === undefined || current.parentId === null
        ? undefined
        : byId.get(current.parentId);
    }
    path.reverse();
    this.branchCache = path;
    this.branchCacheLeaf = session.leafId;
    return [...path];
  }

  // specialSurfaceTui is the TUI handed to a footer/header factory. Upstream
  // passes the real TUI, so a factory may call requestRender or size itself
  // from terminal.columns/rows; pig passed {} and those threw.
  specialSurfaceTui(kind) {
    const self = this;
    return {
      requestRender: () => self.renderSpecialSurface(kind),
      get width()  { return self.ready?.width || 80; },
      get height() { return self.ready?.height || 24; },
      terminal: {
        get columns() { return self.ready?.width || 80; },
        get rows()    { return self.ready?.height || 24; },
      },
    };
  }

  // footerDataProvider mirrors upstream's ReadonlyFooterDataProvider
  // (footer-data-provider.ts:387), the third argument a ui.setFooter factory
  // receives. Upstream hands over an object with methods, not a data bag, and
  // footers call onBranchChange to subscribe.
  footerDataProvider() {
    const self = this;
    return {
      getGitBranch() { return self.state.footerData.gitBranch; },
      getExtensionStatuses() { return new Map(Object.entries(self.state.footerData.extensionStatuses || {})); },
      getAvailableProviderCount() { return self.state.footerData.availableProviderCount || 0; },
      onBranchChange(callback) {
        if (typeof callback !== "function") return () => {};
        self.branchChangeCallbacks.add(callback);
        return () => self.branchChangeCallbacks.delete(callback);
      },
    };
  }

  notifyBranchChange() {
    for (const callback of [...this.branchChangeCallbacks]) {
      try {
        callback();
      } catch (err) {
        this.fireAndForget("ui.notify", {
          message: `footer branch subscriber failed: ${err?.message || String(err)}`,
          level: "error",
        });
      }
    }
  }

  renderSpecialSurface(kind) {
    if (!this.conn) return;
    const factory = kind === "footer" ? this.footerFactory : this.headerFactory;
    // State updates redraw only this extension's installed surfaces. An explicit UI setter owns clearing the shared slot.
    if (!factory) return;
    try {
      // Upstream builds the component once and re-renders it; rebuilding per
      // frame would re-run factory side effects, and a footer that subscribes
      // via onBranchChange would leak one subscriber per render.
      let component = this.specialSurfaceComponents.get(kind);
      if (!component) {
        const tuiShim = this.specialSurfaceTui(kind);
        component = kind === "footer"
          ? factory(tuiShim, this.ui.theme, this.footerDataProvider())
          : factory(tuiShim, this.ui.theme);
        this.specialSurfaceComponents.set(kind, component);
      }
      const width = this.ready.width || 80;
      const lines = component?.render?.(width);
      this.fireAndForget(kind === "footer" ? "ui.setFooter" : "ui.setHeader", {
        clear: false,
        lines: Array.isArray(lines) ? lines.map((v) => String(v)) : [],
        width,
      });
    } catch (err) {
      this.fireAndForget("ui.notify", {
        message: `${kind} render failed: ${err?.message || String(err)}`,
        level: "error",
      });
    }
  }

  // openCustomOverlay implements ctx.ui.custom(factory) over the
  // subprocess bridge. The factory is invoked locally to construct
  // the component; the host opens an overlay shell, the runtime
  // pushes rendered frames via "ui.custom.render" notifications, and
  // each input chunk arrives as a "ui.custom.input" notification.
  // When the component invokes done(value), the runtime sends a
  // "ui.custom.close" notification with the value and resolves the
  // original "ui.custom" host-call promise that the host returns
  // once it tears down the overlay.
  async openCustomOverlay(factory, options = {}) {
    if (typeof factory !== "function") {
      throw new Error("ui.custom requires a factory function");
    }
    this.customOverlaySeq += 1;
    const key = `custom-${this.customOverlaySeq}`;
    const overlay = {
      key,
      component: undefined,
      renderFrame: () => {},
      renderImmediate: () => {},
      reject: undefined,
      seq: 0,
      active: true,
      renderRequested: false,
      renderTimer: undefined,
      lastRenderAt: Number.NEGATIVE_INFINITY,
      disposed: false,
      dispose: () => {},
    };
    this.customOverlays.set(key, overlay);

    let doneCalled = false;
    let resolveValue;
    const valueP = new Promise((resolve) => { resolveValue = resolve; });
    let hostCallStarted = false;
    const closeHost = (args) => {
      if (hostCallStarted) this.notify("ui.custom.close", { key, ...args });
    };
    const done = (value) => {
      if (doneCalled) return;
      doneCalled = true;
      try {
        JSON.stringify(value === undefined ? null : value);
        closeHost({ result: value === undefined ? null : value });
        resolveValue({ value });
      } catch (err) {
        const error = new Error(`encode focused result: ${err?.message || String(err)}`);
        try { closeHost({ error: error.message }); } catch {}
        resolveValue({ error });
      }
    };
    const closeWithError = (reason) => {
      if (doneCalled) return;
      doneCalled = true;
      const error = reason instanceof Error ? reason : new Error(String(reason));
      try { closeHost({ error: error.message }); } catch {}
      resolveValue({ error });
    };
    overlay.reject = closeWithError;

    const sizing = options && typeof options === "object" ? options : {};
    const isOverlay = Boolean(sizing.overlay);
    let overlayOpts;

    let requestFrame = () => {};
    const self = this;
    const tuiShim = {
      requestRender: () => requestFrame(),
      get width()  { return self.ready?.width || 80; },
      get height() { return self.ready?.height || 24; },
      terminal: {
        get columns() { return self.ready?.width || 80; },
        get rows()    { return self.ready?.height || 24; },
      },
    };

    let component;
    overlay.dispose = () => {
      if (overlay.disposed) return;
      overlay.disposed = true;
      try { component?.dispose?.(); } catch {}
    };
    try {
      const themeShim = this.ui?.theme;
      // Pi hands factories its keybindings manager; the extension process
      // has pi-tui's (D73: the user's keybindings.json stays in the host).
      const built = factory(tuiShim, themeShim, getKeybindings(), done);
      component = built && typeof built.then === "function" ? await built : built;
      // showExtensionCustom resolves options only after the factory completes.
      if (!doneCalled) {
        overlayOpts = typeof sizing.overlayOptions === "function"
          ? (isOverlay ? sizing.overlayOptions() : undefined)
          : sizing.overlayOptions;
      }
    } catch (err) {
      this.customOverlays.delete(key);
      throw err instanceof Error ? err : new Error(String(err));
    }
    if (doneCalled) {
      this.customOverlays.delete(key);
      overlay.dispose();
      const outcome = await valueP;
      if (outcome.error) throw outcome.error;
      return outcome.value;
    }
    if (!component) {
      this.customOverlays.delete(key);
      throw new Error("ui.custom factory returned null component");
    }

    overlay.component = component;
    // Pi focuses the component it shows (TUI.setFocus), unless it is a
    // non-capturing overlay, so focusable components such as Input and
    // Editor render their cursor marker.
    if (requireModule("./shims/pi-dist/pi-tui/tui.js").isFocusable(component) && !(isOverlay && overlayOpts?.nonCapturing)) component.focused = true;
    const openArgs = {
      key,
      title: String(overlayOpts?.title || ""),
      widthFraction: Number(overlayOpts?.widthFraction || 0) || 0,
      heightFraction: Number(overlayOpts?.heightFraction || 0) || 0,
      overlay: isOverlay,
      hasHandle: isOverlay && typeof sizing.onHandle === "function",
    };
    // Upstream showOverlay(component, overlayOptions ?? { width: component.width })
    // renders the component at the resolved overlay width, not the terminal
    // width. Serialise the same options so the host resolves the same frame.
    let layoutOpts;
    if (isOverlay) {
      layoutOpts = serializeOverlayOptions(overlayOpts);
      if (!layoutOpts && !sizing.overlayOptions) {
        const w = component?.width;
        if (w) layoutOpts = { width: w };
      }
      if (layoutOpts || !(openArgs.title || openArgs.widthFraction || openArgs.heightFraction)) {
        openArgs.overlayOptions = layoutOpts || {};
      }
    }
    const renderWidth = (termWidth) => {
      if (!openArgs.overlayOptions) return termWidth;
      return resolveOverlayWidth(openArgs.overlayOptions, termWidth);
    };
    let lastLines = [];
    let lastWidth;
    const renderNow = () => {
      if (!overlay.active || doneCalled || !this.customOverlays.has(key)) return;
      // termWidth tags the frame's terminal geometry so the host can drop
      // frames laid out for a stale terminal width; the component itself is
      // rendered at the resolved overlay width.
      const termWidth = this.ready.width || 80;
      const width = renderWidth(termWidth);
      let lines;
      try {
        lines = component.render?.(width);
      } catch (err) {
        closeWithError(new Error(`ui.custom render failed: ${err?.message || String(err)}`));
        return;
      }
      const out = Array.isArray(lines) ? lines.map((value) => String(value)) : [];
      if (termWidth === lastWidth && out.length === lastLines.length && out.every((value, index) => value === lastLines[index])) return;
      lastLines = out;
      lastWidth = termWidth;
      overlay.seq += 1;
      try {
        this.notify("ui.custom.render", { key, lines: out, width: termWidth, seq: overlay.seq });
      } catch (err) {
        closeWithError(err);
      }
    };
    const flushFrame = () => {
      overlay.renderTimer = undefined;
      if (!overlay.active || doneCalled || !overlay.renderRequested) return;
      if (this.conn?.socket?.writableNeedDrain) return;
      overlay.renderRequested = false;
      overlay.lastRenderAt = performance.now();
      renderNow();
      if (overlay.renderRequested) requestFrame();
    };
    requestFrame = () => {
      if (!overlay.active || doneCalled || !this.customOverlays.has(key)) return;
      overlay.renderRequested = true;
      if (overlay.renderTimer !== undefined || this.conn?.socket?.writableNeedDrain) return;
      const elapsed = performance.now() - overlay.lastRenderAt;
      const delay = Math.max(0, REMOTE_RENDER_INTERVAL_MS - elapsed);
      overlay.renderTimer = setTimeout(flushFrame, delay);
    };
    overlay.renderFrame = requestFrame;
    overlay.renderImmediate = () => {
      if (!overlay.active || doneCalled || !this.customOverlays.has(key)) return;
      if (overlay.renderTimer !== undefined) {
        clearTimeout(overlay.renderTimer);
        overlay.renderTimer = undefined;
      }
      overlay.renderRequested = false;
      overlay.lastRenderAt = performance.now();
      renderNow();
    };

    overlay.onMounted = state => {
      if (!overlay.active || overlay.handle) return;
      overlay.handle = mountedOverlayHandle(this, overlay, state);
      try { sizing.onHandle?.(overlay.handle); } catch (error) { closeWithError(error); }
    };
    hostCallStarted = true;
    const hostCallP = this.call("ui.custom", openArgs);
    overlay.renderImmediate();

    let hostError;
    try {
      await hostCallP;
    } catch (err) {
      hostError = err instanceof Error ? err : new Error(String(err));
    } finally {
      overlay.active = false;
      overlay.releaseHandle?.();
      if (overlay.renderTimer !== undefined) clearTimeout(overlay.renderTimer);
      this.customOverlays.delete(key);
      overlay.dispose();
    }
    if (!doneCalled) resolveValue({ value: undefined });
    const outcome = await valueP;
    if (hostError) throw hostError;
    if (outcome.error) throw outcome.error;
    return outcome.value;
  }

  getModel(provider, modelId) {
    return this.models.get(modelRegistryKey(provider, modelId));
  }

  // applyModelRegistryState installs a host registry snapshot: the catalog
  // and the provider, error and registration state ctx.modelRegistry reads.
  applyModelRegistryState(state) {
    if (!state || typeof state !== "object") return;
    if (Array.isArray(state.models)) this.replaceModels(state.models);
    this.registryProviders.clear();
    this.registryState = {
      providers: state.providers && typeof state.providers === "object" ? state.providers : {},
      registered: Array.isArray(state.registered) ? state.registered : [],
      error: typeof state.error === "string" && state.error !== "" ? state.error : undefined,
    };
  }

  // Compose the same built-in, models.json and extension layers as Pi. Auth
  // methods receive the caller's AuthContext; they do not capture host secrets.
  getRegistryProvider(id) {
    const native = this.ctx.modelRegistry.getRegisteredNativeProvider(id);
    if (native) return native;
    const state = this.registryState.providers?.[id];
    const models = [...this.models.values()].filter((model) => model.provider === id);
    if (!state && models.length === 0) return undefined;
    if (!state?.composed) {
      const builtin = piBuiltinProvider(id);
      if (builtin) return builtin;
    }
    const config = this.registeredProviderConfig(id) ?? this.registryState.registered?.find(entry => entry.name === id)?.config ?? state?.extensionConfig;
    // A cell member's re-registration replaces the shared root; Pi recomposes on registration, so a composition is reused only for the root it was built from.
    const cached = this.registryProviders.get(id);
    if (cached && cached.config === config) return cached.provider;
    const provider = piProviderComposer().composeModelProvider(id, piBuiltinProvider(id), { getProvider: () => state?.modelsConfig }, config);
    this.registryProviders.set(id, { config, provider });
    return provider;
  }

  replaceModels(models) {
    this.models.clear();
    for (const raw of Array.isArray(models) ? models : []) {
      const model = normalizeModel(raw);
      if (!model) continue;
      const provider = providerIDFromModel(model);
      const modelId = modelIDFromModel(model);
      if (provider && modelId) this.models.set(modelRegistryKey(provider, modelId), model);
    }
  }

  handleNotify(notify) {
    if (notify.method?.startsWith("ui.editor.")) {
      let args = notify.args;
      try {
        if (typeof args === "string") args = JSON.parse(args);
      } catch {
        return;
      }
      this.editorHost.handleNotify(notify.method, args ?? {});
      return;
    }
    switch (notify.method) {
      case "runtime_input_end":
        quitDrain.inputEnded = true;
        refreshQuitDrain();
        return;
      case "tool_render_release": {
        let args = notify.args;
        try {
          if (typeof args === "string") args = JSON.parse(args);
        } catch {
          return;
        }
        this.toolRenderCards.delete(args?.card);
        return;
      }
      case "autocomplete.release":
        this.autocomplete.release(notify.args?.id);
        return;
      case "provider_release": {
        this.nativeProviderCallbacks.delete(notify.args?.key);
        return;
      }
      case "provider_superseded": {
        // Another process registered this provider after this member; Pi keeps only the latest effective root.
        const name = (typeof notify.args === "string" ? JSON.parse(notify.args) : notify.args)?.name;
        if (typeof name !== "string") return;
        this.forgetProviderOwnership(name);
        for (const table of [this.providerConfigObjects, this.nativeProviderObjects]) {
          if (table.get(name)?.owner === this) table.delete(name);
        }
        return;
      }
      case "model_registry_update": {
        let args = notify.args;
        if (typeof args === "string") args = JSON.parse(args);
        this.applyModelRegistryState(args);
        return;
      }
      case "model_stream_event": {
        let args = notify.args;
        if (typeof args === "string") args = JSON.parse(args);
        this.modelStreams.get(args?.streamId)?.push(args?.event);
        return;
      }
      case "state_update":
        try {
          const args = typeof notify.args === "string" ? JSON.parse(notify.args) : notify.args;
          this.applyState(args?.state ?? args);
        } catch {}
        return;
      case "width_change": {
        let args = notify.args;
        try {
          if (typeof args === "string") args = JSON.parse(args);
        } catch {
          args = {};
        }
        const width = Number(args?.width || 0);
        if (width > 0) {
          this.ready.width = width;
          syncTerminalGeometry(this.ready);
          // After the assignment, so a handler reading ctx.width sees the new value.
          for (const handler of [...this.widthChangeHandlers]) {
            try {
              handler(width);
            } catch {}
          }
          for (const key of this.widgets.keys()) this.renderWidget(key);
          for (const overlay of this.customOverlays?.values() || []) {
            overlay.renderFrame();
          }
          // Headers and footers are frames at a width too; re-render them so
          // the host receives frames for the new width.
          this.renderSpecialSurface("header");
          this.renderSpecialSurface("footer");
          this.editorHost.refresh();
        }
        return;
      }
      case "height_change": {
        let args = notify.args;
        try {
          if (typeof args === "string") args = JSON.parse(args);
        } catch {
          args = {};
        }
        const height = Number(args?.height || 0);
        if (height > 0) {
          this.ready.height = height;
          syncTerminalGeometry(this.ready);
          // Re-render widgets: height-dependent layouts (chain graphs,
          // dashboards) may want to reflow.
          for (const key of this.widgets.keys()) this.renderWidget(key);
          for (const overlay of this.customOverlays?.values() || []) {
            overlay.renderFrame();
          }
          this.editorHost.refresh();
        }
        return;
      }
      case "theme_change": {
        let args = notify.args;
        try {
          if (typeof args === "string") args = JSON.parse(args);
        } catch {
          args = {};
        }
        this.ui.theme.setPalette(args || {});
        for (const key of this.widgets.keys()) this.renderWidget(key);
        for (const overlay of this.customOverlays?.values() || []) overlay.renderFrame();
        this.renderSpecialSurface("header");
        this.renderSpecialSurface("footer");
        this.editorHost.refresh();
        return;
      }
      case "ui.custom.opened": {
        const args = typeof notify.args === "string" ? JSON.parse(notify.args) : notify.args;
        this.customOverlays.get(args?.key)?.onMounted?.(args);
        return;
      }
      case "ui.custom.input": {
        let args = notify.args;
        try {
          if (typeof args === "string") args = JSON.parse(args);
        } catch {
          args = {};
        }
        const overlay = this.customOverlays?.get(args?.key);
        if (!overlay) return;
        if (args.state) overlay.applyHandleState?.(args.state);
        try {
          overlay.component?.handleInput?.(String(args?.data ?? ""));
          overlay.renderImmediate();
        } catch (err) {
          overlay.reject?.(err instanceof Error ? err : new Error(String(err)));
        }
        return;
      }
      default:
        return;
    }
  }

  // registerProvider stores a provider config. When it carries an upstream
  // config.oauth, the login/refreshToken/getApiKey closures are stripped into
  // oauthProviders (they cannot cross the process boundary) and replaced with a
  // serializable capability descriptor the host uses to build its OAuth proxy.
  registerProvider(name, config) {
    if (typeof name !== "string") {
      this.registerNativeProvider(name);
      return;
    }
    if (!config) throw new Error("Provider config is required when registering by name");
    // Pi validates the incoming registration on its own, before it touches the stored configuration, then merges its defined values over the previous effective root (model-runtime.ts:753-766).
    piProviderComposer().validateExtensionProvider(name, piBuiltinProvider(name), this.registryState.providers?.[name]?.modelsConfig, config);
    // A loading member merges over its own queued root, else over the cell's active root, as Pi's flush merges over the effective registration.
    const merged = { ...(this.loadState === "active" ? this.registeredProviderConfig(name) ?? this.foreignProviderConfigForMerge(name) : this.registeredProviderConfigs.get(name) ?? this.providerConfigObjects.get(name)?.config) };
    for (const [key, value] of Object.entries(config)) {
      if (value !== undefined) merged[key] = value;
    }
    this.registryProviders.delete(name);
    this.registeredProviderConfigs.set(name, merged);
    this.nativeProviders.delete(name);
    if (this.loadState === "active") this.transferProviderOwnership(name, merged);
    config = merged;
    if (typeof config.streamSimple === "function") {
      this.providerStreams.set(name, config.streamSimple);
      const {streamSimple, ...serializable} = config;
      config = serializable;
    }
    const oauth = config.oauth;
    if (oauth && typeof oauth === "object") {
      this.oauthProviders.set(name, oauth);
      config = {
        ...config,
        oauth: {
          name: oauth.name || name,
          isSubscription: oauth.isSubscription === true,
          has_login: typeof oauth.login === "function",
          has_refresh: typeof oauth.refreshToken === "function",
          has_get_api_key: typeof oauth.getApiKey === "function",
        },
      };
    }
    this.providers.set(name, config);
    if (this.providersSent) this.fireAndForget("registerProvider", { name, config, configRef: this.providerConfigRef(merged) });
  }

  // Native callbacks remain in their owning process; the host installs reverse-call proxies.
  registerNativeProvider(provider) {
    if (!provider || typeof provider.id !== "string" || !provider.id.trim()) throw new Error("Provider id must not be empty.");
    let key = uuid();
    const previous = this.nativeProviders.get(provider.id);
    if (previous && !this.providersSent) this.nativeProviderCallbacks.delete(this.nativeProviderKeys.get(previous));
    this.nativeProviderKeys.set(provider, key);
    this.nativeProviderCallbacks.set(key, provider);
    const declaration=nativeDeclaration(provider, key);
    this.registeredProviderConfigs.delete(provider.id);
    this.providerStreams.delete(provider.id);
    this.providers.delete(provider.id);
    this.nativeProviders.set(provider.id, provider);
    if (this.loadState === "active") {
      this.transferProviderOwnership(provider.id, undefined);
      this.nativeProviderObjects.set(provider.id, { owner: this, provider });
    }
    for(const [key,model] of this.models) if(model.provider===provider.id)this.models.delete(key);
    for(const model of declaration.models) this.models.set(modelRegistryKey(provider.id,model.id),normalizeModel(model));
    if(this.providersSent) this.fireAndForget("registerProvider",{name:provider.id,config:{},native:declaration});
  }

  unregisterProvider(name) {
    this.registryProviders.delete(name);
    this.providers.delete(name);
    this.providerStreams.delete(name);
    this.oauthProviders.delete(name);
    this.registeredProviderConfigs.delete(name);
    this.nativeProviders.delete(name);
    if (this.loadState === "active") this.transferProviderOwnership(name, undefined);
    if (this.providersSent) {
      // Pi's unregisterProvider removes the registration synchronously (model-runtime.ts:791-797); the Host's registry update arrives later, so every member of this process drops its stale entry now.
      for (const runtime of liveRuntimes) runtime.dropRegistration(name);
      this.dropRegistration(name);
      this.fireAndForget("unregisterProvider", { name });
    }
  }

  dropRegistration(name) {
    this.registryState = { ...this.registryState, registered: (this.registryState.registered ?? []).filter((entry) => entry.name !== name) };
  }

  // The Host forwards its caller's abort to a request that already settled while the extension still holds refreshSignal, until releaseRetainedSignal reports that it does not.
  retainSignal(id, source, refreshSignal) {
    const controller = this.activeRequests.get(id);
    if (!controller || controller.signal !== source) return;
    this.signalControllers.set(source, controller);
    this.retainedSignals.set(id, { source: new WeakRef(source) });
    this.signalRegistry.register(refreshSignal, id);
  }

  retainedController(id) {
    const source = this.retainedSignals.get(id)?.source.deref();
    return source && this.signalControllers.get(source);
  }

  releaseRetainedSignal(id) {
    if (!this.retainedSignals.delete(id)) return;
    this.notify("oauth.signal_release", { request_id: id });
  }

  // dispatchOAuth answers an oauth_* request. The provider is keyed by
  // request.tool because oauth_* frames carry no provider field of their own.
  // signal is the host request's abort signal. Pi passes refreshToken the caller's signal, composed with AbortSignal.timeout(15_000) only by resolveStoredOAuth (auth/resolve.ts:149-153, models.ts:474), and login the interaction's signal (provider-composer.ts:281-292); an extension may retain either after the callback returns, and it stays live and unaborted until its own source fires.
  async dispatchOAuth(id, request, signal) {
    const provider = this.oauthProviders.get(request.tool);
    if (!provider) throw new Error(`unknown oauth provider: ${request.tool}`);
    switch (request.method) {
      case "oauth_login": {
        if (typeof provider.login !== "function") throw new Error("provider does not support login");
        const creds = await provider.login(new OAuthLoginCallbacks(this, signal));
        await this.respond(id, oauthCredsToWire(creds));
        return;
      }
      case "oauth_refresh": {
        if (typeof provider.refreshToken !== "function") throw new Error("provider does not support refresh");
        // The Host says whether the caller is resolveStoredOAuth, which composes the timeout; a refresh from Models.refresh gets the caller's signal alone.
        const refreshSignal = typeof request.signal_timeout_ms === "number" ? AbortSignal.any([signal, AbortSignal.timeout(request.signal_timeout_ms)]) : signal;
        this.retainSignal(id, signal, refreshSignal);
        const creds = await provider.refreshToken(oauthCredsFromWire(request.args), refreshSignal);
        await this.respond(id, oauthCredsToWire(creds));
        return;
      }
      case "oauth_get_api_key": {
        const key = typeof provider.getApiKey === "function"
          ? provider.getApiKey(oauthCredsFromWire(request.args))
          : (request.args?.access ?? "");
        await this.respond(id, { apiKey: String(key ?? "") });
        return;
      }
      default:
        throw new Error(`unknown oauth method: ${request.method}`);
    }
  }

  async handleRequest(id, request, ctx) {
    try {
      switch (request.method) {
        case "facet": {
          await this.respond(id, await dispatchFacet(this, request, ctx));
          return;
        }
        case "provider_object_callback": {
          const result = await dispatchProviderObjectCallback(this, request);
          await this.respond(id, result);
          return;
        }
        case "provider_call":
        case "provider_stream": {
          const result=await dispatchNativeProvider(this,id,request,ctx);
          await this.respond(id,result);
          return;
        }
        case "provider_stream_simple": {
          const callback = this.providerStreams.get(request.tool);
          if (!callback) throw new Error(`unknown provider stream: ${request.tool}`);
          const {model, context, options} = request.args;
          // Pi's composer calls extension.streamSimple with the effective registered root as its receiver (provider-composer.ts:500-501).
          const stream = await callback.call(this.registeredProviderConfig(request.tool), model, context, {...options, signal:ctx.signal});
          for await (const event of stream) {
            if (ctx.signal.aborted) throw new Error("provider stream aborted");
            const accepted = this.conn.send({type:"notify", notify:{method:"provider_stream_event",args:{request_id:id,result:event}}});
            if (!accepted) await new Promise((resolve,reject)=>{
              const finish=(error)=>{this.conn.socket.off("drain",drain);this.conn.socket.off("close",closed);ctx.signal.removeEventListener("abort",aborted);error?reject(error):resolve();};
              const drain=()=>finish();const closed=()=>finish(new Error("extension connection closed"));const aborted=()=>finish(new Error("provider stream aborted"));
              this.conn.socket.once("drain",drain);this.conn.socket.once("close",closed);ctx.signal.addEventListener("abort",aborted,{once:true});if(ctx.signal.aborted)aborted();
            });
          }
          await this.respond(id, await stream.result());
          return;
        }
        case "tool_call": {
          const tool = this.tools.get(request.tool);
          if (!tool) throw new Error(`unknown tool: ${request.tool}`);
          const params = tool.prepareArguments ? tool.prepareArguments(request.args || {}) : (request.args || {});
          // Upstream passes a live AbortSignal and an onUpdate that streams
          // partial results; both end with this request.
          let done = false;
          const onUpdate = (partial) => {
            if (!done) this.notify("tool_update", { request_id: id, result: this.normalizeToolResult(partial) });
          };
          let result;
          try {
            result = await tool.execute?.(request.tool_call_id, params, ctx.signal, onUpdate, ctx);
          } finally {
            done = true;
          }
          await this.respond(id, this.normalizeToolResult(result));
          return;
        }
        case "model_stream_callback": {
          const { streamId, callback, value } = request.args;
          const owned = this.modelStreamCallbacks.get(streamId);
          // Closing an already disposed response is idempotent; a provider's deferred Body.Close may follow its terminal stream event.
          if (!owned && callback === "fetchClose") { await this.respond(id, null); return; }
          const fn = owned?.[callback];
          if (typeof fn !== "function") throw new Error(`unknown model stream callback ${streamId}/${callback}`);
          let result;
          try {
            result = await fn(value, owned.model, ctx.signal);
          } finally {
            // Pi's provider sees the aborted signal before classifying a callback rejection. Wait for the host to apply cancellation before publishing the reverse-call response.
            if (owned.cancellation) await owned.cancellation;
          }
          await this.respond(id, callback === "onPayload" ? { defined: result !== undefined, value: result } : result);
          return;
        }
        case "command_argument_completions": {
          const cmd = this.commands.get(request.tool);
          if (typeof cmd?.getArgumentCompletions !== "function") throw new Error(`command ${request.tool} has no getArgumentCompletions`);
          const items = await cmd.getArgumentCompletions(request.args ?? "");
          await this.respond(id, Array.isArray(items) ? items : null);
          return;
        }
        case "command": {
          const cmd = this.commands.get(request.tool);
          if (!cmd) throw new Error(`unknown command: ${request.tool}`);
          const owner = this.requestContext.getStore();
          if (owner?.id === id) owner.command = true;
          if (owner?.id === id && ctx.mode === "rpc") armRequestWindow(owner, true);
          const pending = cmd.handler?.(request.args ?? "", ctx);
          if (ctx.mode === "rpc") await this.acknowledgeInvocation(id, pending);
          await pending;
          await this.respond(id, null);
          return;
        }
        case "event": {
          const handlers = this.handlers.get(request.event) ?? [];
          const selected = handlers.find(({ id: handlerId }) => handlerId === request.handler_id);
          if (!selected) throw new Error(`unknown event handler ${request.handler_id} for ${request.event}`);
          const event = request.args ?? {};
          // Upstream preparation.fileOps holds Set<string> values; the wire
          // carries arrays (FileOperations.MarshalJSON).
          const fileOps = request.event === "session_before_compact" ? event.preparation?.fileOps : undefined;
          // Upstream hands these handlers an AbortSignal; the host cancels the
          // request when Pi would abort it.
          if (request.event === "session_before_compact" || request.event === "session_before_tree") {
            event.signal = ctx.signal;
          }
          if (fileOps) {
            for (const key of ["read", "written", "edited"]) fileOps[key] = new Set(fileOps[key] ?? []);
          }
          // pig additive (D19): preserve boundary mutations alongside handler errors.
          if (request.event === "agent_before_settle" || request.event === "turn_end") {
            const entries = event.entries;
            let result;
            let error;
            try {
              result = await selected.handler(event, ctx);
            } catch (err) {
              error = err instanceof Error ? err : new Error(String(err));
            }
            await this.respond(id, { _pigBoundaryEntries: entries, _pigBoundaryResult: result }, error);
            return;
          }
          if (request.event === "before_agent_start") {
            const options = event.systemPromptOptions;
            // Pi's normalized options always carry a selectedTools array; the host omits an empty one.
            if (options.selectedTools === undefined) options.selectedTools = [];
            let result;
            let error;
            try {
              result = await selected.handler(event, ctx);
            } catch (err) {
              error = err instanceof Error ? err : new Error(String(err));
            }
            await this.respond(id, { _pigPromptSections: options.sections, _pigPromptSelectedTools: options.selectedTools, _pigPromptResult: result }, error);
            return;
          }
          const messages = event.messages;
          const snapshot = Array.isArray(messages) ? messages.slice() : undefined;
          const pending = selected.handler(event, ctx);
          const shutdownOwner = this.requestContext.getStore();
          if (shutdownOwner?.id === id && shutdownOwner.quit && ctx.mode === "rpc") armRequestWindow(shutdownOwner);
          // Calling an async handler executes its synchronous prefix before returning its Promise. Unawaited notifications admit the next caller at that boundary.
          if (request.event === "ui_prompt_start" || request.event === "ui_prompt_end" || request.event === "session_info_changed") {
            this.conn.requestState(id, "blocked", "external_io");
          }
          if (request.event === "session_shutdown" && ctx.mode === "rpc") await this.acknowledgeInvocation(id, pending);
          let result = await pending;
          if ((request.event === "context" || request.event === "context_with_system") && snapshot) {
            const returned = result?.messages ?? messages;
            result = { messages: returned, _pigContextUnchanged: returned.length === snapshot.length && returned.every((message, i) => message === snapshot[i]) };
          }
          // before_provider_headers handlers mutate event.headers in place and
          // their return value is ignored (runner.ts emitBeforeProviderHeaders);
          // the host cannot share the object, so send the mutated headers back.
          if (request.event === "before_provider_headers") {
            await this.respond(id, event.headers ?? {});
            return;
          }
          if (request.event === "user_bash" && result !== undefined) {
            if (result === null) throw new Error('Invalid user_bash handler result: return undefined for local execution or exactly one valid { operations } or { result } object');
            const undefinedExitCode = result.result && Object.hasOwn(result.result, "exitCode") && result.result.exitCode === undefined;
            result = { ...result, _pigUserBashExitCodeUndefined: Boolean(undefinedExitCode) };
          }
          await this.respond(id, result);
          return;
        }
        case "shortcut": {
          const shortcut = this.shortcuts.get(request.tool);
          if (!shortcut) throw new Error(`unknown shortcut: ${request.tool}`);
          await shortcut.handler?.(ctx);
          await this.respond(id, null);
          return;
        }
        case "render_message": {
          const handler = this.renderers.get(request.tool);
          if (!handler) throw new Error(`unknown renderer: ${request.tool}`);
          const payload = request.args || {};
          const component = await handler(payload.message, payload.options, this.ui.theme);
          const lines = Array.isArray(component) ? component : component?.render?.(payload.width);
          await this.respond(id, { lines: Array.isArray(lines) ? lines : [] });
          return;
        }
        case "markdown_transform": {
          // Pi applies each transformer synchronously while it renders
          // (markdown-transform.ts): a string result replaces the Markdown, and
          // anything else, a throw included, keeps it.
          const payload = request.args || {};
          let transformed = null;
          if (typeof this.markdownTransformer === "function") {
            try {
              const result = this.markdownTransformer(payload.markdown ?? "", payload.context ?? {});
              if (typeof result === "string") transformed = result;
            } catch {}
          }
          await this.respond(id, transformed);
          return;
        }
        case "render_entry": {
          const handler = this.entryRenderers.get(request.tool);
          if (!handler) throw new Error(`unknown entry renderer: ${request.tool}`);
          const payload = request.args || {};
          const component = await handler(payload.entry, payload.options, this.ui.theme);
          const lines = Array.isArray(component) ? component : component?.render?.(payload.width);
          await this.respond(id, { lines: Array.isArray(lines) ? lines : [] });
          return;
        }
        case "render_tool": {
          const tool = this.tools.get(request.tool);
          const payload = request.args || {};
          const isResult = payload.phase === "result";
          const renderer = isResult ? tool?.renderResult : tool?.renderCall;
          if (typeof renderer !== "function") throw new Error(`tool ${request.tool} has no ${isResult ? "renderResult" : "renderCall"}`);
          let card = this.toolRenderCards.get(payload.card);
          if (!card) {
            card = { state: {}, call: undefined, result: undefined };
            this.toolRenderCards.set(payload.card, card);
          }
          const phase = isResult ? "result" : "call";
          let component = card[phase];
          if (payload.rerender || component === undefined) {
            const context = {
              ...(payload.context || {}),
              args: payload.args,
              invalidate: () => this.notify("tool_render_invalidate", { card: payload.card }),
              lastComponent: component,
              state: card.state,
            };
            try {
              component = isResult
                ? renderer({ content: payload.result?.content ?? [], details: payload.result?.details }, payload.options ?? {}, this.ui.theme, context)
                : renderer(payload.args, this.ui.theme, context);
            } catch (err) {
              card[phase] = undefined;
              throw err;
            }
            card[phase] = component;
          }
          const lines = component?.render?.(payload.width);
          await this.respond(id, { lines: Array.isArray(lines) ? lines : [] });
          return;
        }
        case "autocomplete.suggest": {
          await this.respond(id, await this.autocomplete.suggest(request.args, ctx.signal));
          return;
        }
        case "terminal_input": {
          this.state.editorText = request.args.editorText;
          this.state.toolsExpanded = request.args.toolsExpanded;
          // Upstream's TerminalInputHandler is synchronous and the host waits
          // on this reply, so the handlers run inline in registration order.
          // A handler's data replaces the chunk for the handlers after it; a
          // throw degrades to no verdict rather than capturing the keystroke.
          const original = request.args?.data ?? "";
          let current = original;
          let consume = false;
          for (const handler of [...this.terminalInputHandlers]) {
            let result;
            try {
              result = handler(current);
            } catch {
              continue;
            }
            if (result?.consume) {
              consume = true;
              break;
            }
            if (result?.data !== undefined) current = String(result.data);
          }
          await this.respond(id, consume || current === original ? { consume } : { consume, data: current });
          return;
        }
        case "oauth_login":
        case "oauth_refresh":
        case "oauth_get_api_key":
          await this.dispatchOAuth(id, request, ctx.signal);
          return;
        default:
          throw new Error(`unknown request method: ${request.method}`);
      }
    } catch (err) {
      await this.respond(id, null, err instanceof Error ? err : new Error(String(err)));
    }
  }

  // pig additive (D19): normal completion retires async-local request ownership before its response; cancellation never promotes that owner.
  async respond(id, result = null, error = null) {
    const owner = this.requestContext.getStore();
    if (owner?.id === id) {
      owner.cancelled ||= !owner.settled && owner.controller.signal.aborted;
      owner.settled = true;
      if (owner.pendingHostCalls.size > 0) await Promise.all([...owner.pendingHostCalls]);
      owner.responded = true;
      this.conn.cancelParent(id);
    }
    this.conn.respond(id, result, error);
  }

  // The host request whose handler is running the current code. Code the
  // handler left scheduled when it returned (a timer, a promise it did not
  // await) still carries the request's async context, but it is no longer part
  // of that request, whose host calls the host cancels once it ends. As in Pi,
  // where nothing ties a call to the event that scheduled it, such calls are
  // the extension's own: pi-powerline-footer opens its welcome overlay from a
  // timer its session_start handler starts.
  activeRequest() {
    const request = this.requestContext.getStore();
    return request && !request.settled ? request : undefined;
  }

  normalizeToolResult(result) {
    if (result == null) return {};
    if (typeof result === "string") return { content: result };
    if (typeof result !== "object") return { content: String(result) };
    return {
      content: toolContent(result.content ?? result),
      details: result.details,
      is_error: Boolean(result.isError ?? result.is_error),
      terminate: result.terminate === true ? true : undefined,
      usage: result.usage ?? undefined,
    };
  }

  startModelStream(model, context, options = {}, simple = false, apiRequest = false) {
    const streamId = `model-stream-${this.nextModelStreamId++}`;
    const stream = new ModelEventStream();
    this.modelStreams.set(streamId, stream);
    // An AbortSignal (upstream options.signal) cannot cross the process
    // boundary: its abort cancels the host request instead, and the provider
    // ends the stream as upstream does for an aborted request.
    const { signal, onPayload, onResponse, transformHeaders, fetch, ...requestOptions } = options ?? {};
    const fetchCallbacks = typeof fetch === "function" ? modelFetchCallbacks(fetch, signal) : {};
    const callbacks = { model, onPayload, onResponse, transformHeaders, ...fetchCallbacks };
    this.modelStreamCallbacks.set(streamId, callbacks);
    let onAbort;
    if (signal && typeof signal.addEventListener === "function") {
      onAbort = () => { callbacks.cancellation = this.call("cancelModelStream", { streamId }).catch(() => {}); };
      signal.addEventListener("abort", onAbort, { once: true });
    }
    // The receive loop applies preceding stream notifications before this call settles. Cleanup runs outside the host-call continuation and reports a missing terminal event rather than fabricating a successful stream.
    const settle = async (error) => {
      if (!stream.terminal && this.conn?.queue.length > 0) {
        setImmediate(() => { void settle(error); });
        return;
      }
      try { await fetchCallbacks.disposeFetch?.(); }
      catch (cleanupError) { error ??= cleanupError; }
      if (!stream.terminal) stream.fail(error ?? new Error("model stream ended without a terminal event"), model);
      this.modelStreams.delete(streamId);
      this.modelStreamCallbacks.delete(streamId);
      if (onAbort) signal.removeEventListener("abort", onAbort);
    };
    void this.call("modelStream", { streamId, model, simple, apiRequest, fetch: typeof fetch === "function", onPayload: typeof onPayload === "function", onResponse: typeof onResponse === "function", transformHeaders: typeof transformHeaders === "function", request: { ...context, ...requestOptions } }).then(
      () => setImmediate(() => settle()),
      (error) => setImmediate(() => settle(error)),
    );
    if (signal?.aborted) onAbort?.();
    return stream;
  }

  // Only these synchronous operations may wait while the IO worker continues servicing the connection.
  callSync(method, args = {}) {
    const facetMethod = method === "facet.host.sync" && hasFacetBridge(this);
    const providerMethod = method === "provider.object" && ["getModels", "filterModels", "update"].includes(args.method);
    const providerCallback = method === "provider.callback" && args.method === "notify";
    const themeMethod = ["ui.getTheme", "ui.setTheme", "ui.theme", "ui.addAutocompleteProvider", "ui.autocomplete.invoke", "ui.autocomplete.current"].includes(method);
    if (!facetMethod && !themeMethod && !["registerTool", "getAllTools", "getActiveTools", "xref.hello", "xref.op", "provider.config", "events.on", "events.off", "events.emit", "events.settle"].includes(method) && method !== "ui.custom.control" && method !== "provider.retain" && method !== "provider.release" && !providerMethod && !providerCallback) {
      throw new Error(`Host method ${method} is not a synchronous bridge operation`);
    }
    if (!this.conn) throw new Error("runtime not connected");
    const parent = this.activeRequest()?.id ?? "";
    if (parent) this.conn.requestState(parent, "blocked", "host_call");
    try { return this.conn.callSync(method, args, parent); }
    finally { if (parent) this.conn.requestState(parent, "progress"); }
  }

  async call(method, args = {}) {
    const owner = this.requestContext.getStore();
    const connection = owner?.connection ?? this.conn;
    if (!connection || connection.closed || connection !== this.conn) throw new Error("extension connection closed or replaced");
    if (owner?.cancelled || (!owner?.settled && owner?.controller.signal.aborted)) {
      throw new Error("host call cancelled with its parent request");
    }
    const parentRequestId = owner && !owner.settled ? owner.id : "";
    if (parentRequestId && !USER_BLOCKING_CALLS.has(method)) {
      connection.requestState(parentRequestId, "blocked", "host_call");
    }
    try {
      return await connection.call(method, args, parentRequestId);
    } finally {
      if (parentRequestId && !owner.responded && !connection.closed) connection.requestState(parentRequestId, "progress");
    }
  }

  blockForUser() {
    const requestId = this.activeRequest()?.id || "";
    if (requestId) this.conn.requestState(requestId, "blocked", "user");
  }

  // addTerminalInputHandler subscribes to raw terminal input, telling the host
  // to start forwarding only on the first handler and to stop on the last, so
  // an extension that never subscribes costs the input loop nothing.
  addWidthChangeHandler(handler) {
    this.widthChangeHandlers.push(handler);
    let done = false;
    return () => {
      if (done) return;
      done = true;
      const i = this.widthChangeHandlers.indexOf(handler);
      if (i >= 0) this.widthChangeHandlers.splice(i, 1);
    };
  }

  addTerminalInputHandler(handler) {
    this.terminalInputHandlers.push(handler);
    if (this.terminalInputHandlers.length === 1) {
      this.fireAndForget("ui.onTerminalInput", {});
    }
    let done = false;
    return () => {
      if (done) return;
      done = true;
      const i = this.terminalInputHandlers.indexOf(handler);
      if (i >= 0) this.terminalInputHandlers.splice(i, 1);
      if (this.terminalInputHandlers.length === 0) {
        this.fireAndForget("ui.offTerminalInput", {});
      }
    };
  }

  // Message-shaped events carry upstream's flat role-discriminated union:
  //   {"type":"message_end","message":{"role":"assistant","content":[...]}}
  // Content is a block array, never a bare string. Exposed on the api object
  // so every SDK offers the same reading of the same payload.
  static messageRole(data) {
    return data?.message?.role ?? "";
  }

  static messageText(data) {
    const content = data?.message?.content;
    if (typeof content === "string") return content;
    if (!Array.isArray(content)) return "";
    let out = "";
    for (const block of content) {
      if (block?.type === "text" && typeof block.text === "string") out += block.text;
    }
    return out;
  }

  // Host-backed synchronous effects belong to the handler's prefix, not its suspension. Flush them before releasing the caller that awaits invocation. A handler whose Promise has already settled never entered an awaited operation: its response releases the caller with the result, as Pi continues a settled await before its next input event.
  async acknowledgeInvocation(id, pending) {
    let settled = false;
    Promise.resolve(pending).then(() => { settled = true; }, () => { settled = true; });
    await Promise.resolve();
    const owner = this.requestContext.getStore();
    while (owner?.pendingHostCalls.size) await Promise.all([...owner.pendingHostCalls]);
    if (!settled) this.conn.requestState(id, "blocked", "external_io");
  }

  fireAndForget(method, args = {}) {
    if (!this.conn) return;
    // The caller does not await, but the dispatcher owns the operation through
    // its parent request. The response cannot report completed while this call
    // is still blocked, and a host rejection remains visible on the existing
    // stderr path.
    const owner = this.activeRequest();
    const parentRequestId = owner?.id || "";
    // These synchronous RPC effects must reach the host before invocation acknowledgment.
    const synchronousRPC = this.ctx.mode === "rpc";
    if (parentRequestId && !synchronousRPC) this.conn.requestState(parentRequestId, "blocked", "host_call");
    let operation;
    operation = this.conn.call(method, args, parentRequestId).catch((err) => {
      const reason = err && err.message ? err.message : String(err);
      try {
        process.stderr.write(`pig: host call ${method} failed: ${reason}\n`);
      } catch {
        // A closed stderr during shutdown must not escalate into an unhandled
        // rejection in a path the caller never awaits.
      }
    }).finally(() => {
      if (parentRequestId) this.conn.requestState(parentRequestId, "progress");
      owner?.pendingHostCalls.delete(operation);
    });
    owner?.pendingHostCalls.add(operation);
  }

  notify(method, args = {}) {
    // Node→Go fire-and-forget notification (no response expected).
    // Used for streaming surfaces like ui.custom.render and
    // ui.custom.close where each frame is independent.
    if (!this.conn) return;
    this.conn.notify(method, args);
  }
}

// Pi's loader reports a module that exports no factory with this message as
// the whole load error (loader.ts loadExtension).
export function invalidFactory(entry) {
  const error = new Error(`Extension does not export a valid factory function: ${entry}`);
  error.pigLoaderError = true;
  return error;
}

// loadFailure is Pi's load error for a factory or import that threw:
// "Failed to load extension: <message>" (loader.ts loadExtension), with the
// thrown error's stack for diagnostics.
export function loadFailure(err) {
  if (err?.pigLoaderError) return { error: err.message, stack: err.stack || "" };
  const message = err instanceof Error ? err.message : String(err);
  return { error: `Failed to load extension: ${message}`, stack: err instanceof Error ? err.stack || "" : "" };
}

// reportLoadFailure connects to the host, sends a load_failed notify in place
// of the register handshake, and closes. In a packed cell it always settles:
// destroy() tears the connection down after the frame is written whether or
// not the host has accepted the connection yet (an end() would wait for the
// host to close its side, and the host may still be working through earlier
// members). An isolated extension's process exits once this settles, so it
// waits for the host to read the frame and close the connection; otherwise
// the host could see the exit before the connection.
export function reportLoadFailure(sockPath, failure, { waitForHost = false } = {}) {
  return new Promise((resolve) => {
    if (!sockPath) {
      resolve();
      return;
    }
    const data = Buffer.from(JSON.stringify({ type: "notify", notify: { method: "load_failed", args: failure } }));
    const header = Buffer.alloc(4);
    header.writeUInt32BE(data.length, 0);
    const socket = net.createConnection(sockPath, () => {
      if (waitForHost) socket.end(Buffer.concat([header, data]));
      else socket.write(Buffer.concat([header, data]), () => socket.destroy());
    });
    socket.on("error", () => resolve());
    socket.on("close", () => resolve());
  });
}

// 0.3.0: replaced by Pi runner wiring
export async function loadExtension(entry) {
  const runtime = new Runtime(entry);
  try {
    const install = await importExtension(entry);
    if (typeof install !== "function") throw invalidFactory(entry);
    await install(runtime.api);
  } catch (err) {
    runtime.discardLoad();
    if (runtime.earlyConn) await runtime.reportLoadFailureOnConnection(loadFailure(err));
    else await reportLoadFailure(process.env.PIG_EXT_SOCKET, loadFailure(err), { waitForHost: true });
    process.exit(1);
  }
  runtime.commitLoad();
  await runtime.run();
}
