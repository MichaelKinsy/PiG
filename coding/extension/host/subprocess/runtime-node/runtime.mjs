import { modelFetchCallbacks } from "./model-fetch.mjs";
import { AutocompleteRuntime } from "./autocomplete.mjs";
import { dispatchFacet, dispatchFacetSync, hasFacetBridge } from "./facet-bridge.mjs";
import { syncTerminalGeometry } from "./terminal-geometry.mjs";
import { mountedOverlayHandle } from "./overlay-handle.mjs";
import { disposeIndependentSessions } from "./independent-session-owner.mjs";
import { dispatchNativeProvider, nativeDeclaration } from "./native-provider.mjs";
import { dispatchProviderObjectSync, dispatchProviderObjectCallback, remoteProvider } from "./provider-object.mjs";
import { isWaiting, ProviderSocket, setProviderSocketsRef, shareDecodedFrames } from "./provider-socket.mjs";
import { MessageChannel } from "node:worker_threads";
import net from "node:net";
import { closeSync, openSync, readSync } from "node:fs";
import { basename } from "node:path";
import { AsyncLocalStorage } from "node:async_hooks";
import { importExtension } from "./jiti-loader.mjs";
import { randomUUID } from "node:crypto";
import { inspect } from "node:util";
import { createRequire, flushCompileCache } from "node:module";
import { stringWidget } from "./widget-component.mjs";
import { currentRuntime, runWithRuntime, setRuntime } from "./state.mjs";
import { getKeybindings, KeybindingsManager, setKeybindings } from "./shims/pi-dist/pi-tui/keybindings.js";
import { EditorComponentHost } from "./editor-component.mjs";
import { setCapabilities as setTerminalCapabilities } from "./shims/pi-dist/pi-tui/terminal-image.js";
import { loadAllHighlightLanguages } from "./shims/syntax-highlight.mjs";
import { themeFromPalette, wireColors } from "./theme-palette.mjs";
import { Chalk } from "./shims/chalk/source/index.js";
import { classifierErrorResult, imageErrorResult } from "./shims/pi-dist/pi-ai/utils/model-operations.js";
import { backgroundAnsi, foregroundAnsi, parseColor, styleTextWithAnsi } from "./shims/pi-dist/pi-tui/colors.js";
import { initTheme, setThemeInstance } from "./shims/pi-dist/pi-coding-agent/modes/interactive/theme/theme.js";
import { createExtensionRuntime } from "./shims/pi-dist/pi-coding-agent/core/extensions/loader.js";
import { installCloneHooks, Realm } from "./xref.mjs";
import { SharedBus } from "./event-bus.mjs";
import { deriveView, surfaceTheme, VIEW_THEME_TOKENS } from "./view-walk.mjs";
import { dispatchOverlayMouse, takesMouse } from "./mouse.mjs";

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
// The host sends each model registry publication to every member of a cell. Its handler only reads the snapshot, and applyModelRegistryState copies what an extension can reach, so the members share one decoded publication.
shareDecodedFrames((envelope) => envelope?.type === "notify" && envelope.notify?.method === "model_registry_update");

// Marks the renderers of a definition from Pi's create<Tool>ToolDefinition
// (shims/builtin-tools.mjs): the host draws those halves with its built-in
// renderers for the named tool.
const builtInToolRenderer = Symbol.for("pig.builtInToolRenderer");

// A host call the runtime cancels (its parent request was cancelled, or its connection closed on shutdown, reload or session replacement) rejects with an error made here. Pi never rejects a host call that way: its calls are in-process and outlive the command that started them (interactive-mode.ts showExtensionCustom 2858-2940 settles only through done() or a factory failure). An extension that dropped the call's promise, as pi-mcp-adapter drops ctx.ui.custom's, owns nothing that can observe this rejection, so it must not end the process, which a packed cell shares with the successor generation of a reload. Every other unhandled rejection keeps Node's default (lib/internal/process/promises.js, --unhandled-rejections=throw): an extension listener handles it, and with none it raises as an uncaught exception, as in Pi, whose process the extension shares.
const hostCancellations = new WeakSet();

function hostCancelled(message) {
  const error = new Error(message);
  hostCancellations.add(error);
  return error;
}

// A run that ends because the host closed its connection (normal exit, reload, replacement) is a shutdown, not a runtime failure.
export function isHostCancellation(error) {
  return hostCancellations.has(error);
}

process.on("unhandledRejection", (reason) => {
  if (hostCancellations.has(reason)) return;
  // Node raises only when no listener handles the event; this listener is registered before any extension loads.
  if (process.listenerCount("unhandledRejection") > 1) return;
  // Node's isErrorLike: an object with its own stack is raised as is.
  if (typeof reason === "object" && reason !== null && Object.hasOwn(reason, "stack")) throw reason;
  const error = new Error(`This error originated either by throwing inside of an async function without a catch block, or by rejecting a promise which was not handled with .catch(). The promise rejected with the reason "${inspect(reason)}".`);
  error.code = "ERR_UNHANDLED_REJECTION";
  throw error;
});

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

// catalogOf returns a registry snapshot's catalog: each model's normalized form under its registry key, in the order the runtime has always kept them (a key keeps its first position and takes its last model). The members of a cell receive one decoded snapshot (shareDecodedFrames below), so the catalog is built once per snapshot, and each member reads its own copy of a model (Runtime.modelFor) as an isolated extension holds its own.
const catalogs = new WeakMap();
const emptyCatalog = new Map();
function catalogOf(models) {
  if (!Array.isArray(models)) return emptyCatalog;
  let catalog = catalogs.get(models);
  if (catalog) return catalog;
  catalog = new Map();
  for (const raw of models) {
    const model = normalizeModel(raw);
    if (!model) continue;
    const provider = providerIDFromModel(model);
    const modelId = modelIDFromModel(model);
    if (provider && modelId) catalog.set(modelRegistryKey(provider, modelId), model);
  }
  catalogs.set(models, catalog);
  return catalog;
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

// indexSessionEntry adds one log entry to upstream's byId, labelsById and labelTimestampsById.
function indexSessionEntry(index, entry) {
  if (!entry || entry.type === "session") return;
  index.byId.set(entry.id, entry);
  if (entry.type !== "label") return;
  if (entry.label) {
    index.labelsById.set(entry.targetId, entry.label);
    index.labelTimestampsById.set(entry.targetId, entry.timestamp);
  } else {
    index.labelsById.delete(entry.targetId);
    index.labelTimestampsById.delete(entry.targetId);
  }
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
    // The host confirms a pending entry only with a new replicated log, so while the log is unchanged an append only extends the view, as upstream's appendCustomEntry only pushes onto its log.
    const view = this._view;
    if (view && view.base === entries && view.baseLength === entries.length && view.pending === pending) {
      for (let i = view.pendingLength; i < pending.length; i++) view.entries.push(pending[i]);
      view.pendingLength = pending.length;
      return view.entries;
    }
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
    if (unconfirmed.length === 0) {
      this._view = undefined;
      return entries;
    }
    const combined = entries.concat(unconfirmed);
    this._view = { base: entries, baseLength: entries.length, pending: this.runtime.pendingEntries, pendingLength: unconfirmed.length, entries: combined };
    return combined;
  }

  // _index mirrors upstream's byId, labelsById and labelTimestampsById,
  // rebuilt when the log changes.
  _index() {
    const base = this.runtime.state.session?.entries;
    const entries = this._entries();
    const cached = this._cachedIndex;
    // The log only grows while it stays the same array, so an append indexes only the new entries.
    if (cached && cached.base === base && cached.entries === entries && cached.count <= entries.length) {
      for (let i = cached.count; i < entries.length; i++) indexSessionEntry(cached, entries[i]);
      cached.count = entries.length;
      return cached;
    }
    const index = { base, entries, count: entries.length, byId: new Map(), labelsById: new Map(), labelTimestampsById: new Map() };
    for (const entry of entries) indexSessionEntry(index, entry);
    this._cachedIndex = index;
    return index;
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
  getCwd() { return this._info().cwd ?? this.runtime.contextValues.cwd; }
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
  // event marks pi.appendEntry, after which the host emits entry_appended as upstream's runtime action does (agent-session.ts:3406-3411); a direct ctx.sessionManager.appendCustomEntry only writes the log.
  appendCustomEntry(customType, data, event = false) {
    const entry = {
      type: "custom",
      customType,
      data,
      id: generateEntryId(this._index().byId),
      parentId: this._leafId(),
      timestamp: new Date().toISOString(),
    };
    this.runtime.pendingEntries.push(entry);
    const direct = { id: entry.id, timestamp: entry.timestamp };
    if (event) direct.event = true;
    this.runtime.fireAndForget("appendEntry", { customType, data, direct });
    return entry.id;
  }
}

// ReplacedSessionManager is the ReadonlySessionManager of a withSession context (types.ts:442, session-manager.ts:245-262). It reads the replacement Session through synchronous sessionRead host calls of the with_session request, which the host serves from the replacement Session.
class ReplacedSessionManager {
  constructor(runtime, parent) { this.runtime = runtime; this.parent = parent; }
  _read(method, args) { return this.runtime.conn.callSync("sessionRead", { method, args }, this.parent()); }
  _optional(method, args) { return this._read(method, args) ?? undefined; }
  getCwd() { return this._read("getCwd"); }
  getSessionDir() { return this._read("getSessionDir"); }
  getSessionId() { return this._read("getSessionId"); }
  getSessionFile() { return this._optional("getSessionFile"); }
  getSessionName() { return this._optional("getSessionName"); }
  getLeafId() { return this._read("getLeafId") ?? null; }
  getLeafEntry() { return this._optional("getLeafEntry"); }
  getEntry(id) { return this._optional("getEntry", { id }); }
  getLabel(id) { return this._optional("getLabel", { id }); }
  getBranch(fromId) { return this._read("getBranch", { fromId }) ?? []; }
  getHeader() { return this._read("getHeader") ?? null; }
  getEntries() { return this._read("getEntries") ?? []; }
  getTree() { return this._read("getTree") ?? []; }
  buildContextEntries() { return this._read("buildContextEntries") ?? []; }
  buildSessionProjection() { return this._read("buildSessionProjection"); }
}

// SetupSessionManager is the SessionManager of newSession's setup callback (types.ts:411, agent-session-runtime.ts:254-257): the reads of ReplacedSessionManager and the appends that seed the replacement Session. Each append is a synchronous sessionWrite host call of the setup request and returns the new entry's id, as Pi's do.
class SetupSessionManager extends ReplacedSessionManager {
  _write(method, args) { return this.runtime.conn.callSync("sessionWrite", { method, args }, this.parent()); }
  appendMessage(message) { return this._write("appendMessage", { message }); }
  appendCustomEntry(customType, data) { return this._write("appendCustomEntry", { customType, data }); }
  appendCustomMessageEntry(customType, content, display, details) { return this._write("appendCustomMessageEntry", { customType, content, display, details }); }
  appendSessionInfo(name) { return this._write("appendSessionInfo", { name }); }
  appendModelChange(provider, modelId) { return this._write("appendModelChange", { provider, modelId }); }
  appendThinkingLevelChange(thinkingLevel) { return this._write("appendThinkingLevelChange", { thinkingLevel }); }
  appendLabelChange(targetId, label) { return this._write("appendLabelChange", { targetId, label: label ?? null }); }
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
  getAll() { return [...this.runtime.allModels().values()]; }
  getAvailable() { return this.getAll().filter((model) => {const state=this._providerState(model.provider);return state?.configured===true&&(!state.availableModelIds||state.availableModelIds.includes(model.id))}); }
  find(provider, modelId) {
    return this.runtime.getModel(provider, modelId);
  }
  // model-registry.ts:71-79,135-177. Chat models come from the registry snapshot's models and the others from its typedModels, in the host's order (model-registry.ts:145-161); getAvailableOfType and classify ask the host.
  findOfType(type, provider, modelId) { return this.getModelOfType(type, provider, modelId); }
  getModelsOfType(type, provider) {
    const typed = this.runtime.registryState.typedModels ?? [];
    return [...this.getAll(), ...typed].filter((model) => (model.type ?? "chat") === type && (provider === undefined || model.provider === provider));
  }
  async getAvailableOfType(type, provider, options) {
    return (await this.runtime.call("getAvailableOfType", { type, provider })) ?? [];
  }
  getModelOfType(type, provider, modelId) { return this.getModelsOfType(type, provider).find((model) => model.id === modelId); }
  // Never rejects: a failure, and an aborted request, are a result that names the model (model-registry.ts:168-177, model-runtime.ts:800-815). The result is aborted when the caller's signal aborted or, as in the Go, Rust and Python SDKs, when the calling request was cancelled.
  async classify(model, context, options) {
    const signal = options?.signal;
    const owner = this.runtime.requestContext.getStore();
    let onAbort;
    try {
      // The signal and any callback of the options cannot cross the process boundary.
      const { signal: _signal, ...rest } = options ?? {};
      const sent = options === undefined ? undefined : JSON.parse(JSON.stringify(rest));
      const aborted = new Promise((_, reject) => {
        const reason = () => reject(signal.reason ?? new Error("The operation was aborted"));
        if (signal?.aborted) reason();
        else if (signal) { onAbort = reason; signal.addEventListener("abort", onAbort, { once: true }); }
      });
      aborted.catch(() => {});
      const request = this.runtime.call("classify", { model, context, options: sent });
      return await (signal ? Promise.race([request, aborted]) : request);
    } catch (error) {
      return classifierErrorResult(model, error, signal?.aborted === true || owner?.controller?.signal.aborted === true);
    } finally {
      if (onAbort) signal.removeEventListener("abort", onAbort);
    }
  }
  // Never rejects: a failure, and an aborted request, are a result that names the model (model-registry.ts:181-188, model-runtime.ts:783-798). Like classify, the result is aborted when the caller's signal aborted or the calling request was cancelled.
  async generateImages(model, context, options) {
    const signal = options?.signal;
    const owner = this.runtime.requestContext.getStore();
    let onAbort;
    try {
      // The signal and any callback of the options cannot cross the process boundary.
      const { signal: _signal, ...rest } = options ?? {};
      const sent = options === undefined ? undefined : JSON.parse(JSON.stringify(rest));
      const aborted = new Promise((_, reject) => {
        const reason = () => reject(signal.reason ?? new Error("The operation was aborted"));
        if (signal?.aborted) reason();
        else if (signal) { onAbort = reason; signal.addEventListener("abort", onAbort, { once: true }); }
      });
      aborted.catch(() => {});
      const request = this.runtime.call("generateImages", { model, context, options: sent });
      return await (signal ? Promise.race([request, aborted]) : request);
    } catch (error) {
      return imageErrorResult(model, error, signal?.aborted === true || owner?.controller?.signal.aborted === true);
    } finally {
      if (onAbort) signal.removeEventListener("abort", onAbort);
    }
  }
  // model-registry.ts:211-213 and model-runtime.ts:955-963: the facade's registrations are the runtime's, and its route gets the request alone. pi.registerVirtualModel's route also gets a context (loader.ts:493-501).
  registerVirtualModel(definition) { this.runtime.registerVirtualModel(Object.assign(Object.create(definition), { route: (request) => definition.route(request) })); }
  unregisterVirtualModel(provider, id) { this.runtime.unregisterVirtualModel(provider, id); }
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

// The chalk ThemeShim draws modifiers with: whether they draw is the host's
// palette.modifiers (chalk's level for the host's stdout), not this pipe's.
const modifierChalk = new Chalk({ level: 1 });
export class ThemeShim {
  constructor() {
    this.foregrounds = {};
    this.backgrounds = {};
    // Upstream's bold, italic, underline, inverse and strikethrough are chalk
    // styles, which chalk drops when the host's stdout has no color support.
    this.modifiers = true;
    // The host resolves the appearance and the concrete colors with the palette (they depend on the terminal's reported colors, which this process cannot see). Before a palette there is none.
    this.appearance = undefined;
    this.colors = Object.freeze({});
  }
  setPalette(palette = {}) {
    this.foregrounds = palette.foregrounds && typeof palette.foregrounds === "object" ? palette.foregrounds : {};
    this.backgrounds = palette.backgrounds && typeof palette.backgrounds === "object" ? palette.backgrounds : {};
    this.modifiers = palette.modifiers !== false;
    this.name = typeof palette.name === "string" ? palette.name : undefined;
    this.sourcePath = typeof palette.sourcePath === "string" && palette.sourcePath !== "" ? palette.sourcePath : undefined;
    this.mode = palette.mode === "256color" ? "256color" : "truecolor";
    this.appearance = palette.appearance === "light" || palette.appearance === "dark" ? palette.appearance : undefined;
    this.colors = wireColors(palette.colors);
    this.tokenColorPalettes = undefined;
    setThemeInstance(this);
  }
  // withTokenColors runs fn with the given [token, "#rrggbb"] entries
  // overriding the palette, as the host derives a view's surface theme with
  // tui.Theme.WithTokenColors (D107 §13.3): each color escaped in the
  // palette's color mode, a faint token staying faint. The palette is
  // restored afterwards, unless fn installed another one.
  withTokenColors(entries, fn) {
    const saved = { foregrounds: this.foregrounds, backgrounds: this.backgrounds, colors: this.colors };
    const derived = this.tokenColorPalette(entries);
    Object.assign(this, derived);
    try {
      return fn();
    } finally {
      if (this.foregrounds === derived.foregrounds) Object.assign(this, saved);
    }
  }
  // tokenColorPalette is the overridden palette, kept per override so a
  // steady surface reuses one palette (the view walk caches by its identity).
  tokenColorPalette(entries) {
    const key = JSON.stringify(entries);
    this.tokenColorPalettes ??= new Map();
    let derived = this.tokenColorPalettes.get(key);
    if (derived) return derived;
    const mode = this.getColorMode();
    const foregrounds = { ...this.foregrounds };
    const backgrounds = { ...this.backgrounds };
    const colors = { ...this.colors };
    for (const [token, value] of entries) {
      const color = parseColor(value);
      colors[token] = color;
      if (VIEW_THEME_TOKENS[token] === "bg") backgrounds[token] = backgroundAnsi(color, mode);
      else foregrounds[token] = foregroundAnsi(color, mode) + (this.foregrounds[token]?.endsWith?.("\x1b[2m") ? "\x1b[2m" : "");
    }
    derived = { foregrounds, backgrounds, colors: Object.freeze(colors) };
    // A surface whose override changes every frame (a track color) must not grow this without bound.
    if (this.tokenColorPalettes.size >= 32) this.tokenColorPalettes.clear();
    this.tokenColorPalettes.set(key, derived);
    return derived;
  }
  // Upstream Theme.style over the palette's escape sequences (theme.ts:342-367). The attributes are SGR drawn directly, whatever the host's chalk level is.
  style(text, options) {
    const { fg, bg } = options;
    // The palette's foreground appends SGR 2 to a faint token's color; a color never ends in it.
    const faint = typeof fg === "string" && this.tokenAnsi(this.foregrounds, fg).endsWith("\x1b[2m");
    const fgAnsi = fg === undefined ? undefined : typeof fg === "string" ? this.tokenAnsi(this.foregrounds, fg) : foregroundAnsi(fg, this.getColorMode());
    const bgAnsi = bg === undefined ? undefined : typeof bg === "string" ? this.tokenAnsi(this.backgrounds, bg) : backgroundAnsi(bg, this.getColorMode());
    return styleTextWithAnsi(String(text ?? ""), faint ? fgAnsi.slice(0, -"\x1b[2m".length) : fgAnsi, bgAnsi, faint ? { ...options, dim: true } : options);
  }
  tokenAnsi(tokens, token) {
    const ansi = Object.hasOwn(tokens, token) ? tokens[token] : undefined;
    if (typeof ansi !== "string" || ansi === "") throw new Error(`Unknown theme color: ${token}`);
    return ansi;
  }
  // Upstream's modifiers are chalk's (theme.ts bold, italic, ...): chalk
  // re-opens a style after an inner close of it and around each line break.
  modifier(style, text) {
    const value = String(text ?? "");
    return this.modifiers ? modifierChalk[style](value) : value;
  }
  // The host sends each foreground as Pi's getFgAnsi opening, which ends in SGR 2 for a faint token (theme.ts:399-402). Pi's fg closes a faint token with SGR 22;39 (theme.ts:363), so the faint attribute does not leak past the text.
  fg(token, text) {
    const value = String(text ?? "");
    const open = this.foregrounds[token] || "";
    if (!open) return this.unknownOrUnstyled(token, value);
    return `${open}${value}${open.endsWith("\x1b[2m") ? "\x1b[22;39m" : "\x1b[39m"}`;
  }
  bg(token, text) {
    const value = String(text ?? "");
    const open = this.backgrounds[token] || "";
    return open ? `${open}${value}\x1b[49m` : this.unknownOrUnstyled(token, value);
  }
  // A token the palette lacks is Pi's `Unknown theme color` throw (theme.ts:374); a theme the host has not sent a palette to has no tokens to lack.
  unknownOrUnstyled(token, value) {
    if (Object.keys(this.foregrounds).length === 0 && Object.keys(this.backgrounds).length === 0) return value;
    throw new Error(`Unknown theme color: ${token}`);
  }
  bold(text) { return this.modifier("bold", text); }
  dim(text) { return `\x1b[2m${String(text ?? "")}\x1b[22m`; }
  italic(text) { return this.modifier("italic", text); }
  underline(text) { return this.modifier("underline", text); }
  inverse(text) { return this.modifier("inverse", text); }
  strikethrough(text) { return this.modifier("strikethrough", text); }
  // Upstream Theme.getFgAnsi, getBgAnsi, getColorMode, getThinkingBorderColor
  // and getBashModeBorderColor over the host's palette.
  getFgAnsi(color) {
    const ansi = this.foregrounds[color];
    if (!ansi) throw new Error(`Unknown theme color: ${color}`);
    return ansi;
  }
  getBgAnsi(color) {
    const ansi = this.backgrounds[color];
    if (!ansi) throw new Error(`Unknown theme color: ${color}`);
    return ansi;
  }
  getColorMode() { return this.mode ?? "truecolor"; }
  getThinkingBorderColor(level) {
    const token = { off: "thinkingOff", minimal: "thinkingMinimal", low: "thinkingLow", medium: "thinkingMedium", high: "thinkingHigh", xhigh: "thinkingXhigh", max: "thinkingMax" }[level] ?? "thinkingOff";
    return (text) => this.fg(token, text);
  }
  getBashModeBorderColor() { return (text) => this.fg("bashMode", text); }
}

// pig additive (D19): after RPC stdin ends, the transport must not keep an otherwise drained Node event loop alive. Report the drain to the process owner without resolving any pending extension Promise. A process reports when its loop drained with a pending quit session_shutdown handler (runtime_drained: Pi's exit when its loop empties), and, when it has none, with an unanswered command it has not reported yet (runtime_commands_drained: the host then knows the process cannot answer the commands it had started, as Pi's single loop would not either). A command that arrives after a report makes the loop reportable again.
const quitDrain = { active: false, inputEnded: false, reported: false, listening: false, tailCheck: false, runtimes: new Set() };

function quitDrainState() {
  let outstanding = false;
  let commands = false;
  let tail = false;
  let hostWaits = false;
  for (const runtime of quitDrain.runtimes) {
    for (const request of runtime.requestRecords.values()) {
      if (request.responded) continue;
      outstanding ||= request.quit;
      commands ||= request.command && !request.drainReported;
      tail ||= request.quitTail;
    }
    for (const pending of runtime.conn?.pending.values() ?? []) {
      if (pending.synchronous || !USER_BLOCKING_CALLS.has(pending.method)) hostWaits = true;
    }
  }
  return { outstanding, commands, tail, hostWaits };
}

function refreshQuitDrain() {
  if (!quitDrain.inputEnded) return;
  const { outstanding, commands, tail, hostWaits } = quitDrainState();
  const quitWaits = quitDrain.active && !quitDrain.reported && outstanding;
  setProviderSocketsRef(!(quitWaits || commands) || hostWaits);
  // Pi flushRawStdout lets immediately fulfilled Promise continuations run, but does not await a post-disposal handler's future timer or I/O. IPC must not turn that suspension into a Session join.
  if (quitDrain.active && !quitDrain.reported && tail && !outstanding && !hostWaits && !quitDrain.tailCheck) {
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
  ...USER_BLOCKING_CALLS, "exec", "modelStream", "getModelAuth", "getProviderAuth", "getAvailableOfType", "classify", "generateImages", "refreshModelRegistry", "compact", "waitForIdle",
  "setModel", "newSession", "fork", "navigateTree", "switchSession", "reload", "oauth.cb.onSelect", "oauth.cb.onPrompt", "oauth.cb.onManualCodeInput",
]);

function windowPending(owner) {
  for (const pending of owner.connection.pending.values()) {
    if (pending.parentRequestId === owner.id && !WINDOW_CLOSING_CALLS.has(pending.method)) return true;
  }
  return false;
}

// armRequestWindow closes the window of a command or of a quit session_shutdown handler at the next check phase after its handler's synchronous prefix, or once its pending short host calls settle. A command arms it before its handler runs with early set: its immediate is then queued ahead of every immediate the handler and its microtask continuations queue in this iteration, and it closes a MessagePort, whose close callback runs in this iteration's closing phase, after the check phase and before the next poll phase. A threadpool completion, a nested immediate and a timer therefore land after the window closes.
// pig divergence (D85): a continuation that runs after this window closes can still execute in this runtime process until the host stops it, and the window cannot see a suspended session_shutdown handler of another runtime process; Pi runs everything on one event loop and exits.
function armRequestWindow(owner, early = false) {
  const close = () => {
    if (owner.responded || owner.turned) return;
    if (windowPending(owner)) {
      owner.windowWait = true;
      return;
    }
    owner.turned = true;
    // A command's suspension decides only what stdin's end makes of it. Reporting it before then would latch a continuation that Pi runs before it reads stdin's end, so the report waits for runtime_input_end (releaseCommandSuspensions).
    if (owner.command && !owner.quit && !quitDrain.inputEnded) {
      owner.unreportedAt = hostCallEpoch(owner);
      return;
    }
    if (!owner.connection.closed) owner.connection.requestState(owner.id, "suspended");
  };
  if (early) {
    closeInCloseCallbacks(close);
  } else setImmediate(close);
}

// hostCallEpoch counts the host calls the request started or finished, so a command whose window closed can be told apart from one that has run since.
function hostCallEpoch(owner) {
  return owner.connection?.callEpochs?.get(owner.id) ?? 0;
}

// pig additive (D19): Pi's commands share one event loop with stdin, so it has no report to defer; this runtime is a separate process and must tell the host what Pi's loop would have done at stdin's end.
// releaseCommandSuspensions applies runtime_input_end to the commands whose window closed unresponded earlier. Pi reads stdin's end in the iteration after the line that starts a command only when the client closes stdin at once; a client that closes it later (on seeing the handler's notification, say) finds every continuation that ran in between answered. A command that started or finished a host call since its window closed ran meanwhile, so it gets a fresh window from here: it is suspended again only if it still waits when that window closes. A command that did nothing since is suspended now.
function releaseCommandSuspensions() {
  for (const runtime of quitDrain.runtimes) {
    for (const request of runtime.requestRecords.values()) {
      if (request.unreportedAt === undefined || request.responded) continue;
      const at = request.unreportedAt;
      request.unreportedAt = undefined;
      if (hostCallEpoch(request) !== at) {
        request.turned = false;
        armRequestWindow(request);
      } else if (!request.connection.closed) {
        request.connection.requestState(request.id, "suspended");
      }
    }
  }
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

// resumeRequestWindows rearms the windows that waited for short host calls, and the turn window of a request whose handler started or finished a host call since it last turned the event loop.
function resumeRequestWindows() {
  for (const runtime of quitDrain.runtimes) {
    for (const request of runtime.requestRecords.values()) {
      if (request.windowWait && !windowPending(request)) {
        request.windowWait = false;
        if (request.turnWindow) armTurnWindow(request);
        else armRequestWindow(request);
      } else if (request.turnedAt !== undefined && request.turnedAt !== hostCallEpoch(request) && !windowPending(request)) {
        request.turnedAt = undefined;
        armTurnWindow(request);
      }
    }
  }
}

function drainQuitRequests() {
  if (!quitDrain.inputEnded) return;
  const { outstanding, commands, hostWaits } = quitDrainState();
  if (hostWaits) return;
  if (outstanding && quitDrain.active && !quitDrain.reported) reportQuitExit("runtime_drained");
  else if (commands) reportQuitExit("runtime_commands_drained");
}

function reportQuitExit(method) {
  if (!quitDrain.inputEnded) return;
  if (method !== "runtime_commands_drained") {
    if (!quitDrain.active || quitDrain.reported) return;
    quitDrain.reported = true;
  }
  // The report covers the commands the process started before it: the host marks those whose started state precedes the report on their connection. A drained loop with a pending quit handler cannot answer its commands either.
  if (method !== "runtime_quit_yield") {
    for (const runtime of quitDrain.runtimes) {
      for (const request of runtime.requestRecords.values()) {
        if (request.command && !request.responded) request.drainReported = true;
      }
    }
  }
  setProviderSocketsRef(true);
  // Every hosted connection reports, so the host has read each connection's earlier frames, such as a command's response, before it acts on the drain.
  for (const runtime of quitDrain.runtimes) {
    if (runtime.conn && !runtime.conn.closed) runtime.conn.notify(method);
  }
}

function listenForQuitDrain() {
  if (quitDrain.listening) return;
  quitDrain.listening = true;
  process.on("beforeExit", drainQuitRequests);
}

function enterQuitDrain() {
  quitDrain.active = true;
  listenForQuitDrain();
}

// Pi's print and JSON modes await each prompt and go on in the same continuation: to the next prompt, or to disposing the runtime, which runs session_shutdown and then invalidates the runner (print-mode.ts:131-166, agent-session-runtime.ts:404-410, agent-session.ts:1393-1406). A prompt ends with its command, its handled input or agent_settled, so a timer, immediate or I/O callback an extension queued runs only once the next prompt reaches the agent's I/O, or after the ctx went stale. This process answers the host over IPC, which would give the event loop such a callback before the host's next step arrives: pi-goal-x's agent_settled continuation timer then started a run during disposal, which failed with the stale message. So in those modes, after answering a call that can end a prompt, or session_shutdown, while no run is in progress, the process stays in that call until the host's next step reaches it: a request, a run beginning, invalidate, reload_started, loop_turned, shutdown or a closed connection. Every Node generation of the process waits together, packed or isolated alike. State the host replicates meanwhile is applied in order after the wait.
// The host sends invalidate and a run's run_signal frames to every connection, and a packed process reads its sockets in no fixed order: one member's copy can arrive after another member's, or after a later frame of another member. So invalidate and a run beginning end the wait only once every open connection of the process has read its own copy, and only a run newer than every run the process had read of before the wait begins one: a member that reads a finished run's frames late, before its first request, is not reading a run beginning. run is the newest run the process has read of on any connection; baseRun and broadcast are the wait's.
const PROMPT_END_MODES = new Set(["print", "json"]);
const PROMPT_END_EVENTS = new Set(["input", "agent_settled", "session_shutdown"]);
const hostStep = { waiting: false, arrived: false, run: 0, baseRun: 0, broadcast: new Set() };

// Pi's runner awaits each extension's handler in turn, on one event loop (runner.ts:1089-1116). When a later extension's handler awaits a timer, I/O or a child process, that loop turns and runs the callbacks an earlier extension queued, with a live ctx, before the handler resumes. A packed process shares that loop. Another process's handler suspends on its own loop, so in print and JSON mode a request's turn window tells the host when its handler yielded to the event loop: the window closes at the first check phase after the handler's synchronous run and microtasks (it is armed before the handler runs, ahead of every immediate the handler queues), or once the handler's pending short host calls settle (windowPending: Pi settles those in microtasks). If the request is unanswered then, the runtime sends loop_turned, and the host forwards it to every other Node process as its next step. A host call the request starts or finishes after that is its handler running again, so the request gets a fresh window once its calls settle. A request that arrives during a run arms no window: the run's I/O already turns Pi's loop, and the run beginning ended every wait.
function armTurnWindow(owner) {
  owner.turnWindow = true;
  setImmediate(() => {
    if (owner.settled || owner.connection.closed) return;
    if (windowPending(owner)) {
      owner.windowWait = true;
      return;
    }
    owner.turnedAt = hostCallEpoch(owner);
    try {
      owner.connection.notify("loop_turned");
    } catch {
      // The connection closed meanwhile; the host forwards nothing for it.
    }
  });
}

// A loop_turned that ends this process's wait stands for the other handler's yield. On Pi's one loop every immediate queued before that yield runs before anything the handler queues itself, so before its continuation and, after session_shutdown, the runner's invalidate. The host's frames after the turn follow that continuation, and they can arrive with the turn itself. So the process delivers each frame that arrives after the turn from an immediate it queues on the turn's arrival: the immediates queued before the turn run first, with a live ctx. Timers get no such order: on Pi's loop an earlier extension's timer runs before the continuation only when the other handler waits for a later timer or I/O (it runs after one that waits for an immediate), and here it runs by the clock. A connection that closes delivers its held frames at once.
const turnHold = { immediate: undefined, connections: new Set() };

function holdAfterTurn() {
  if (turnHold.immediate !== undefined) return;
  turnHold.immediate = setImmediate(() => {
    turnHold.immediate = undefined;
    for (const conn of turnHold.connections) conn.releaseHeld();
    turnHold.connections.clear();
  });
}

// declaredMode is the mode the host declared in its ready payload, the run mode it drives. ctx.mode defaults an omitted mode to "print" (runner.ts:361), but a host that declares none, such as an embedding SDK host, has not promised print mode's continuation, so its runtime does not wait.
// runActive is whether the host's run_signal, synced before every request, held a run in progress. A call answered during a run, such as the input of a steer or follow-up an extension sends with sendUserMessage (agent-session.ts:1968-2000, 2365-2394), ends no prompt: Pi's prompt() queues the message and returns while the run goes on doing I/O, so Pi's event loop turns.
function endsPrompt(request, declaredMode, runActive) {
  if (!PROMPT_END_MODES.has(declaredMode) || runActive) return false;
  return request?.method === "command" || (request?.method === "event" && PROMPT_END_EVENTS.has(request.event));
}

// noteHostStep records env, read on conn, toward the host's next step: a request, shutdown or reload_started is the step; invalidate or a run beginning is the step once every open connection of the process has read its copy.
function noteHostStep(conn, env) {
  let broadcast = false;
  if (env.type === "notify" && env.notify?.method === "run_signal") {
    let args = env.notify.args;
    try {
      if (typeof args === "string") args = JSON.parse(args);
    } catch {
      return;
    }
    const run = typeof args?.run === "number" ? args.run : 0;
    broadcast = args?.active === true && run > hostStep.baseRun;
    if (run > hostStep.run) hostStep.run = run;
    if (!broadcast) return;
  } else if (env.type === "notify" && env.notify?.method === "invalidate") {
    broadcast = true;
  } else if (env.type !== "request" && env.type !== "shutdown" && !(env.type === "notify" && env.notify?.method === "reload_started")) {
    return;
  }
  if (!hostStep.waiting) return;
  if (broadcast) {
    hostStep.broadcast.add(conn);
    if (!broadcastRead()) return;
  }
  hostStep.arrived = true;
}

// broadcastRead reports whether every open connection of the process has read the broadcast step the wait has begun to read.
function broadcastRead() {
  if (hostStep.broadcast.size === 0) return false;
  for (const runtime of liveRuntimes) {
    const conn = runtime.conn;
    if (conn && !conn.closed && !hostStep.broadcast.has(conn)) return false;
  }
  return true;
}

// awaitHostStep returns at once while a request of this process is unanswered: its continuation needs the event loop, as Pi's would.
function awaitHostStep(conn) {
  if (conn.closed || isWaiting()) return;
  for (const runtime of liveRuntimes) {
    for (const request of runtime.requestRecords.values()) {
      if (!request.responded) return;
    }
  }
  hostStep.waiting = true;
  hostStep.arrived = false;
  hostStep.baseRun = hostStep.run;
  hostStep.broadcast.clear();
  try {
    conn.socket.waitUntil(() => hostStep.arrived || conn.closed);
  } catch {
    // The connection closed while waiting; nothing remains to order.
  } finally {
    hostStep.waiting = false;
    hostStep.broadcast.clear();
  }
}

export class Connection {
  constructor(socket) {
    this.socket = socket;
    this.pending = new Map();
    this.callEpochs = new Map();
    this.queue = [];
    this.waiters = [];
    this.held = [];
    this.closed = false;
    this.streamStarts = new Map();
    socket.on("envelope", env => this.onEnvelope(env));
    socket.on("close", () => this.onClose());
    socket.on("error", () => this.onClose());
  }

  onClose() {
    if (this.closed) return;
    this.closed = true;
    // A member whose connection closes has no copy left to read.
    if (hostStep.waiting && !hostStep.arrived && broadcastRead()) hostStep.arrived = true;
    this.releaseHeld();
    for (const waiter of this.waiters.splice(0)) waiter(null);
    for (const [id, pending] of this.pending) {
      // A received result still belongs to the ordered receive loop, even if the peer closes before that loop reaches it.
      if (pending.responseQueued) continue;
      this.pending.delete(id);
      pending.reject(hostCancelled("connection closed"));
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
    if (env.type === "notify" && env.notify?.method === "loop_turned") {
      // Another Node process's handler turned the event loop (armTurnWindow). Only a process that waits for the host's next step acts on it; the frames after it wait for the turn (holdAfterTurn).
      if (hostStep.waiting && !hostStep.arrived) {
        hostStep.arrived = true;
        holdAfterTurn();
      }
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
      // The loading factory's connection has no serve loop yet: its awaited calls settle as their results arrive.
      if (pending?.synchronous || pending?.appliedOnReceipt || (pending && this.loading)) {
        this.resolveCall(env);
        return;
      }
      if (pending) pending.responseQueued = true;
    }
    noteHostStep(this, env);
    if (turnHold.immediate !== undefined) {
      this.held.push(env);
      turnHold.connections.add(this);
      return;
    }
    this.deliver(env);
  }

  deliver(env) {
    if (this.waiters.length > 0) this.waiters.shift()(env);
    else this.queue.push(env);
  }

  releaseHeld() {
    for (const env of this.held.splice(0)) this.deliver(env);
  }

  resolveCall(env) {
    const pending = this.pending.get(env.id);
    if (!pending) return;
    this.pending.delete(env.id);
    this.touchRequest(pending.parentRequestId);
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

  touchRequest(requestId) {
    const epoch = this.callEpochs.get(requestId);
    if (epoch !== undefined) this.callEpochs.set(requestId, epoch + 1);
  }

  call(method, args = {}, parentRequestId = "") {
    if (this.closed) return Promise.reject(new Error("extension connection closed"));
    const id = uuid();
    return new Promise((resolve, reject) => {
      this.touchRequest(parentRequestId);
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

  // An asynchronous call whose reply is applied when the frame arrives, for a connection nothing reads yet: a loading factory's.
  callAppliedOnReceipt(method, args = {}) {
    if (this.closed) return Promise.reject(new Error("extension connection closed"));
    const id = uuid();
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject, parentRequestId: "", method, appliedOnReceipt: true });
      try {
        this.send({ type: "call", id, call: { method, args } });
      } catch (error) {
        this.pending.delete(id);
        reject(error);
      }
    });
  }

  callSync(method, args = {}, parentRequestId = "") {
    const id = uuid();
    let done = false, value, error;
    this.touchRequest(parentRequestId);
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
    this.touchRequest(parentRequestId);
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
      pending.reject(hostCancelled(`host call cancelled with parent request ${parentRequestId}`));
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

// requestSignal keys a request's own AbortSignal on its context. Pi's ctx.signal is the run's (runner.ts:917-920), so the request's cancellation travels under this key, which no extension can enumerate or collide with.
export const requestSignal = Symbol("pig.requestSignal");

// Pi's createContext (runner.ts:868-960) and createCommandContext (runner.ts:981-1016) guard every member with runner.assertActive(), so a context kept past a stale runtime throws the runner's message instead of answering. Here a member throws once extensionRuntime is stale; the runtime's own code reads the values it holds in runtime.contextValues, which no guard stands in front of.
class RuntimeContext {
  constructor(runtime) {
    bindOwnMethods(this);
    this.runtime = runtime;
    const active = () => runtime.extensionRuntime.assertActive();
    const guarded = (read) => ({ enumerable: true, get: () => { active(); return read(); } });
    // Pi defaults to print until the host binds a mode (runner.ts:357).
    const values = runtime.contextValues = {
      cwd: runtime.ready.cwd || process.cwd(),
      mode: runtime.ready.mode || "print",
      model: undefined,
      sessionManager: new RuntimeSessionManager(runtime),
      modelRegistry: new RuntimeModelRegistry(runtime),
    };
    Object.defineProperties(this, {
      scopedModels: guarded(() => runtime.state.scopedModels ?? []),
      ui: guarded(() => runtime.state.hasUI ? runtime.ui : runtime.noOpUI),
      hasUI: guarded(() => runtime.state.hasUI),
      cwd: guarded(() => values.cwd),
      mode: guarded(() => values.mode),
      sessionManager: guarded(() => values.sessionManager),
      modelRegistry: guarded(() => values.modelRegistry),
      model: guarded(() => values.model),
      thinkingLevel: guarded(() => runtime.state.thinkingLevel || undefined),
      // runner.ts:917-920: the signal of the run in progress, one object for the whole run, undefined while no run is active. The Host replicates it with run_signal frames (applyRunSignal).
      signal: guarded(() => runtime.runSignal?.controller.signal),
    });
    this.theme = runtime.ui.theme;
    values.model = normalizeModel(runtime.state.model) ?? (runtime.ready.model ? normalizeModel({ id: runtime.ready.model }) : undefined);
  }

  // Live terminal geometry. Getters rather than snapshots so a resize is
  // visible without rebuilding the context. width mirrors the host's
  // width_change notification; height mirrors height_change.
  get width() { return Number(this.runtime.ready?.width || 0); }
  get height() { return Number(this.runtime.ready?.height || 0); }

  isIdle() { this.runtime.extensionRuntime.assertActive(); return this.runtime.state.isIdle; }
  isProjectTrusted() { this.runtime.extensionRuntime.assertActive(); return this.runtime.state.projectTrusted; }
  abort() { this.runtime.extensionRuntime.assertActive(); this.runtime.fireAndForget("abort", {}); }
  hasPendingMessages() { this.runtime.extensionRuntime.assertActive(); return this.runtime.state.hasPendingMessages; }
  // Pi's shutdown() stops the TUI only after the frame the current input
  // produced is painted (it drains input first), so a shutdown requested
  // from an editor's handleInput reaches the host after that frame.
  shutdown() { this.runtime.extensionRuntime.assertActive(); queueMicrotask(() => this.runtime.fireAndForget("shutdown", {})); }
  getContextUsage() { this.runtime.extensionRuntime.assertActive(); return this.runtime.state.contextUsage; }
  // The host reads the options flat. Upstream starts compaction without
  // awaiting it and reports the outcome through onComplete/onError, so a call
  // with callbacks carries no parent request and outlives its handler
  // (agent-session.ts:3369-3378), while the handler that waits for them is
  // reported blocked so the events behind it are dispatched.
  compact(options = {}) {
    this.runtime.extensionRuntime.assertActive();
    const { onComplete, onError, ...rest } = options ?? {};
    if (typeof onComplete !== "function" && typeof onError !== "function") {
      this.runtime.fireAndForget("compact", rest);
      return;
    }
    void this.runtime.call("compact", { ...rest, awaitCompletion: true }, { detached: true }).then(
      (result) => { if (typeof onComplete === "function") onComplete(result); },
      (err) => { if (typeof onError === "function") onError(err instanceof Error ? err : new Error(String(err))); },
    );
  }
  getSystemPrompt() { this.runtime.extensionRuntime.assertActive(); return this.runtime.state.systemPrompt; }
  getSystemPromptOptions() { this.runtime.extensionRuntime.assertActive(); return this.runtime.state.systemPromptOptions ?? {}; }
  // runner.ts:993-1016: the command context's methods throw synchronously when stale, so none is async.
  waitForIdle() { this.runtime.extensionRuntime.assertActive(); return this.runtime.call("waitForIdle", {}); }
  newSession(options = {}) {
    this.runtime.extensionRuntime.assertActive();
    const { withSession, setup, ...rest } = options ?? {};
    return this.runtime.callNewSession(rest, setup, withSession);
  }
  fork(entryId, options = {}) {
    this.runtime.extensionRuntime.assertActive();
    const { withSession, ...rest } = options ?? {};
    return this.runtime.callReplacement("fork", { ...rest, entryId }, withSession);
  }
  navigateTree(targetId, options = {}) { this.runtime.extensionRuntime.assertActive(); return this.runtime.call("navigateTree", { ...options, targetId }); }
  switchSession(sessionPath, options = {}) {
    this.runtime.extensionRuntime.assertActive();
    return this.runtime.callReplacement("switchSession", { sessionPath }, options?.withSession);
  }
  reload() { this.runtime.extensionRuntime.assertActive(); return this.runtime.call("reload", {}); }
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
    // pig divergence (D2): sprite validation remains owned by the host.
    registerSprite: ui.registerSprite,
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
      if (this.runtime.contextValues.mode !== "tui") {
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
    this.runtime.specialSurfaceComponents.get("footer")?.component?.dispose?.();
    this.runtime.footerFactory = typeof factory === "function" ? factory : undefined;
    this.runtime.specialSurfaceComponents.delete("footer");
    if (this.runtime.footerFactory) this.runtime.renderSpecialSurface("footer", true);
    else this.runtime.fireAndForget("ui.setFooter", { clear: true });
  }
  setHeader(factory) {
    this.runtime.specialSurfaceComponents.get("header")?.component?.dispose?.();
    this.runtime.headerFactory = typeof factory === "function" ? factory : undefined;
    this.runtime.specialSurfaceComponents.delete("header");
    if (this.runtime.headerFactory) this.runtime.renderSpecialSurface("header", true);
    else this.runtime.fireAndForget("ui.setHeader", { clear: true });
  }
  // pig additive (D60): Pig accepts typed login data across the subprocess wire.
  async setLogin(definition) { return this.runtime.call("ui.setLogin", definition); }
  // pig divergence (D2): an extension's sprite joins /sprite.
  async registerSprite(definition) { return this.runtime.call("ui.registerSprite", definition); }
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
// dismissed the host prompt. It mirrors the Go/Rust/Python SDK cancel sentinels
// and carries Pi's "Login cancelled", the rejection of a cancelled login prompt
// (interactive-mode.ts:6243-6300 showAuthSelect, showAuthPrompt).
class OAuthBridgeCancelled extends Error {
  constructor() {
    super("Login cancelled");
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
// Value-returning callbacks resolve the host reply; a host cancel rejects each of
// them with "Login cancelled", as Pi's adaptOAuth routes them to the login
// interaction's prompt (provider-composer.ts:361-364).
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
    if (result?.cancel) throw new OAuthBridgeCancelled();
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
    // withSession callbacks of replacement calls in flight, by the handle the call names.
    this.withSessionCallbacks = new Map();
    this.nextWithSessionHandle = 0;
    this.setupCallbacks = new Map();
    this.nextSetupHandle = 0;
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
    // Registrations made while the factory runs. Upstream queues them and applies them in order when the factory succeeds (loader.ts:259-262, 456-497), so a queued unregister removes an earlier queued registration. The Host applies them when it accepts this extension.
    this.mcpServersPending = new Map();
    this.virtualModelsPending = [];
    // The unregistrations the factory made, in call order. Pi filters the runtime-wide queue of models registered before the runner binds (loader.ts:228-232); the Host applies these to it.
    this.virtualModelUnregistrations = [];
    // The virtual models this extension registered, by provider and id. The declaration travels to the Host; the route stays here and runs when the Host routes a request (loader.ts:480-490).
    this.virtualModels = new Map();
    // ctx.executeTool calls in flight, by the id the Host knows them by.
    this.nestedCalls = new Map();
    // BashOperations objects a user_bash reply named by handle; the host releases one when it drops the object.
    this.bashOperations = new Map();
    this.bashOperationsSeq = 0;
    this.nextExecuteId = 1;
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
    // The image and classifier implementations of each provider config (types.ts:1896-1903). They cannot cross the process boundary: the register frame names their APIs and the host calls back with provider_operation.
    this.providerOperations = new Map();
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
    this.toolRendererResolvers = [];
    this.resolvedToolRenderers = new Map();
    this.resolvedToolRendererCount = 0;
    // Upstream ToolExecutionComponent keeps one renderer state per tool card,
    // shared by renderCall and renderResult, and each renderer's last
    // component. The host names the card and releases it when the card is gone.
    this.toolRenderCards = new Map();
    this.modelStreams = new Map();
    this.modelStreamCallbacks = new Map();
    // ctx.modelRegistry's catalog and this extension's own copies of the models it has read (modelFor), every model once it has read them all.
    this.catalog = emptyCatalog;
    this.models = new Map();
    this.modelsComplete = true;
    this.nextModelStreamId = 1;
    this.footerFactory = undefined;
    this.headerFactory = undefined;
    this.widgets = new Map();
    this.customOverlays = new Map();
    this.branchChangeCallbacks = new Set();
    this.specialSurfaceComponents = new Map();
    this.specialSurfaceSeq = 0;
    this.customOverlaySeq = 0;
    this.ready = { cwd: process.cwd(), width: 80, model: "" };
    this.runSignal = undefined;
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
      // pig additive (D107): a D91 frontend draws the interactive mode (StatePayload.frontend); surfaces derive views only then.
      frontend: false,
      // Replicated by the Host: pi.getSettings() reads it (StatePayload.settings). The MCP server registry and ctx.tools are read from the Host when they are read.
      settings: undefined,
      footerData: { gitBranch: null, extensionStatuses: {}, availableProviderCount: 0 },
    };
    this.autocomplete = new AutocompleteRuntime(this);
    // pig additive (D107): image refs whose bytes this connection has sent; ui.view.evicted forgets them.
    this.viewImageRefs = new Set();
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
      registerToolRenderer: (resolver) => this.registerToolRenderer(resolver),
      registerProvider: (name, config) => this.registerProvider(name, config),
      unregisterProvider: (name) => this.unregisterProvider(name),
      sendMessage: action((message, options = {}) => this.fireAndForget("sendMessage", { message, options })),
      sendUserMessage: action((content, options = {}) => this.fireAndForget("sendUserMessage", { content, options })),
      // Upstream appends to the in-process log at once (agent-session.ts:3406-3411), so a read in the same handler sees the entry.
      appendEntry: action((customType, data) => { this.contextValues.sessionManager.appendCustomEntry(customType, data, true); }),
      messageRole: (data) => Runtime.messageRole(data),
      messageText: (data) => Runtime.messageText(data),
      getSessionName: action(() => this.state.session?.sessionName || undefined),
      setSessionName: action((name) => this.fireAndForget("setSessionName", { name })),
      setLabel: action((entryId, label) => this.fireAndForget("setLabel", { entryId, label })),
      exec: (command, args = [], options = undefined) => this.exec({ command, args, options }),
      getFlag: (name) => {
        if (!this.flags.has(name)) return undefined;
        return (Object.hasOwn(this.state.flags, name) ? this.state.flags[name] : undefined) ?? this.flagValues.get(name);
      },
      getActiveTools: action(() => this.callSync("getActiveTools")?.tools || []),
      // Tool registries can change in another extension while this handler is running.
      // The host call also carries PiG's legacy per-tool "source" for the Go,
      // Rust and Python SDKs; Pi's ToolInfo has only sourceInfo, so drop it.
      getAllTools: action(() => (this.callSync("getAllTools")?.tools || []).map(({ source: _legacySource, ...tool }) => tool)),
      // loader.ts:411-414: a copy of the effective settings, from the state the Host replicated.
      getSettings: action(() => {
        if (this.state.settings === undefined) throw new Error("settings are not available: the host has not sent them");
        return structuredClone(this.state.settings);
      }),
      registerMcpServer: (name, config) => this.registerMcpServer(name, config),
      unregisterMcpServer: (name) => this.unregisterMcpServer(name),
      getMcpServers: () => this.getMcpServers(),
      registerVirtualModel: (model) => this.registerVirtualModel(model),
      unregisterVirtualModel: (provider, id) => this.unregisterVirtualModel(provider, id),
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
    this.providerOperations.delete(name);
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

  // A host call the factory may make before the extension registers: it opens the connection a loading factory needs, as the shared event bus does.
  factoryCall(method, args) {
    if (this.conn) return this.callSync(method, args);
    this.ensureConnectionSync();
    return this.earlyConn.callSync(method, args, "");
  }

  // loader.ts:411-414: pi.exec spawns the child whenever it is called. A callback the factory scheduled can call it after the factory returned and before the registered connection exists, while connect() opens it. That call waits for the register frame and runs as the loader's exec; opening a second connection would leave the call or the registration on a socket the Host does not read.
  exec(request) {
    if (this.conn) return this.call("exec", request);
    if (this.loadState === "loading") return this.loadingExec(request);
    if (this.connecting) return this.connecting.then(() => this.call("exec.loading", request));
    return this.call("exec", request);
  }

  // loader.ts:411-414 gives a loading factory an exec that spawns the child itself, with no session bound. The Host runs it in the loader's working directory over the connection the factory opened. The Host's reply is applied when it arrives: nothing reads this connection's queue before the register frame, and the factory awaits the call.
  loadingExec(args) {
    this.ensureConnectionSync();
    return this.earlyConn.callAppliedOnReceipt("exec.loading", args);
  }

  // loader.ts:456-468. The Host validates the config with Pi's own rules and refuses a name another extension owns, and Pi does both when the call is made: the throw reaches the factory, which may catch it. A registration the factory makes is queued once it passed, and reaches the Host in the register frame, as Pi commits it when the factory returns; later ones apply at once.
  registerMcpServer(name, config) {
    const declaration = { name, config: structuredClone(config) };
    // A replaced name keeps its position, as the registry's Map does (mcp-servers.ts:212-215).
    if (!this.conn) {
      this.factoryCall("mcpServers.check", declaration);
      this.mcpServersPending.set(name, declaration.config);
      return;
    }
    this.callSync("registerMcpServer", declaration);
  }

  // loader.ts:472-475. A server another extension registered is left alone by the Host.
  unregisterMcpServer(name) {
    if (!this.conn) {
      this.mcpServersPending.delete(name);
      return;
    }
    this.callSync("unregisterMcpServer", { name });
  }

  // loader.ts:475-478. The live registry: the servers earlier-loaded extensions committed, and not the ones this factory has only queued.
  getMcpServers() {
    return this.factoryCall("getMcpServers", {})?.servers ?? [];
  }

  // loader.ts:480-490. The declaration carries the VirtualModelDefinition fields but route (virtual-models.ts:84-102); the Host calls the route with virtual_model_route. Pi ignores any other property of the object, so none travels: the Host rejects unknown register-frame fields.
  registerVirtualModel(model) {
    const declaration = {
      provider: model.provider,
      id: model.id,
      name: model.name,
      thinkingLevels: model.thinkingLevels,
      contextWindow: model.contextWindow,
      maxTokens: model.maxTokens,
      input: model.input,
    };
    const key = JSON.stringify([model.provider, model.id]);
    const previous = this.virtualModels.get(key);
    this.virtualModels.set(key, model);
    if (!this.conn) {
      this.virtualModelsPending.push(declaration);
      return;
    }
    try {
      this.callSync("registerVirtualModel", declaration);
    } catch (error) {
      if (previous === undefined) this.virtualModels.delete(key);
      else this.virtualModels.set(key, previous);
      throw error;
    }
  }

  // loader.ts:492-495.
  unregisterVirtualModel(provider, id) {
    const key = JSON.stringify([provider, id]);
    if (!this.conn) {
      this.virtualModelsPending = this.virtualModelsPending.filter((declaration) => declaration.provider !== provider || declaration.id !== id);
      this.virtualModelUnregistrations.push({ provider, id });
    } else {
      this.callSync("unregisterVirtualModel", { provider, id });
    }
    this.virtualModels.delete(key);
  }

  // runner.ts:952-985 (createToolContext). The context of a tool call also lists the tools executeTool can call and runs one. An explicit signal replaces the calling tool's, so the call is not tied to the calling request; without one the Host cancels the call with the request.
  addToolContext(ctx, toolCallId) {
    const runtime = this;
    return Object.defineProperties(ctx, {
      tools: {
        get() {
          runtime.extensionRuntime.assertActive();
          return runtime.callSync("getCallableTools")?.tools ?? [];
        },
      },
      executeTool: {
        value: async (name, args, options = {}) => {
          runtime.extensionRuntime.assertActive();
          return runtime.executeTool(toolCallId, name, args, options ?? {});
        },
      },
    });
  }

  async executeTool(toolCallId, name, args, options) {
    const executeId = `e${this.nextExecuteId++}`;
    // runner.ts:979-981 (`options.signal ?? signal`): a null or undefined signal leaves the call with the calling tool's.
    const signal = options.signal ?? undefined;
    const nested = { onUpdate: typeof options.onUpdate === "function" ? options.onUpdate : undefined, signal, hasFailure: false, failure: undefined };
    this.nestedCalls.set(executeId, nested);
    const payload = { callerId: toolCallId, name, args, executeId, ...(nested.onUpdate ? { wantsUpdates: true } : {}), ...(signal !== undefined ? { ownSignal: true } : {}) };
    let stopListening;
    try {
      const outcome = this.call("executeTool", payload, { detached: signal !== undefined });
      if (signal !== undefined) {
        // After the call is sent: the Host applies a cancel only after the call it names started.
        const cancel = () => { this.conn?.call("executeTool.cancel", { executeId }, "").catch(() => {}); };
        if (signal.aborted) cancel();
        else {
          signal.addEventListener("abort", cancel, { once: true });
          stopListening = () => signal.removeEventListener("abort", cancel);
        }
      }
      // The Host rejects a call whose onUpdate threw, after the tool returned (nested-tool-calls.ts:219-248); the call rejects with the error the callback threw, as Pi's rejection carries that object.
      try { return await outcome; }
      catch (error) { throw nested.hasFailure ? nested.failure : error; }
    } finally {
      stopListening?.();
      this.nestedCalls.delete(executeId);
    }
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
      output_schema: tool.outputSchema,
      exposure: tool.exposure,
      namespace: tool.namespace,
      default_active: tool.defaultActive,
      prepares_loadout: typeof tool.prepareLoadout === "function" || undefined,
      prepares_arguments: typeof tool.prepareArguments === "function" || undefined,
      source: tool.source,
      render_shell: tool.renderShell === "self" ? "self" : undefined,
      renders_call: (typeof tool.renderCall === "function" && !tool.renderCall[builtInToolRenderer]) || undefined,
      renders_result: (typeof tool.renderResult === "function" && !tool.renderResult[builtInToolRenderer]) || undefined,
      builtin_renderers: tool.renderCall?.[builtInToolRenderer] ?? tool.renderResult?.[builtInToolRenderer],
    };
  }

  registerCommand(name, definition) {
    // upstream: packages/coding-agent/src/core/extensions/loader.ts:302-311 (#10054)
    if (typeof name !== "string" || name.length === 0) {
      throw new Error(`Command registered by extension "${this.entry}" must have a non-empty string name. Use pi.registerCommand("name", { description, handler }).`);
    }
    if (typeof definition?.handler !== "function") {
      throw new Error(`Command "/${name}" registered by extension "${this.entry}" must define handler().`);
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

  // pi.registerToolRenderer: resolvers run in registration order; a registration after loading reports the count.
  registerToolRenderer(resolver) {
    this.toolRendererResolvers.push(resolver);
    if (this.conn) this.notify("tool_renderers", { count: this.toolRendererResolvers.length });
  }

  // resolve_tool_renderers: the host evaluated next() and sends what it draws. pig divergence (D89): next() returns a
  // marker the host recognizes, so a resolver can keep next()'s renderers but not call them.
  resolveToolRenderers(payload) {
    const marker = payload?.next ? { renderShell: payload.next.render_shell || undefined } : undefined;
    const resolvers = [...this.toolRendererResolvers];
    const resolve = (index) => index < resolvers.length ? resolvers[index](payload.tool, () => resolve(index + 1)) : marker;
    const got = resolve(0);
    if (got === undefined || got === null) return { use: "none" };
    // A marker given render functions is the resolver's own renderers.
    if (got === marker && typeof got.renderCall !== "function" && typeof got.renderResult !== "function") return { use: "next" };
    const id = `r${++this.resolvedToolRendererCount}`;
    this.resolvedToolRenderers.set(id, got);
    return {
      use: "own",
      render_shell: got.renderShell === "self" ? "self" : undefined,
      renders_call: typeof got.renderCall === "function" || undefined,
      renders_result: typeof got.renderResult === "function" || undefined,
      renderers: id,
    };
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
    this.earlyConn.loading = true;
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
      if (this.earlyConn) {
        this.conn = this.earlyConn;
        this.conn.loading = false;
      } else {
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
          argument_completions: typeof cmd.getArgumentCompletions === "function" || undefined,
        })),
        shortcuts: [...this.shortcuts.values()].map((s) => ({ key: s.key, description: s.description || "" })),
        handlers: [...this.handlers.entries()].flatMap(([event, handlers]) =>
          handlers.filter(({ removed }) => !removed).map(({ id }) => ({ event, can_block: BLOCKING_EVENTS.has(event), handler_id: id })),
        ),
        flags: this.flagRegistrations.map((f) => ({ name: f.name, description: f.description || "", type: f.type || "string", default: f.default })),
        providers: [...this.providers.entries()].map(([name, config]) => this.providerDeclaration(name, config)).concat([...this.nativeProviders.values()].map(provider=>({name:provider.id,config:{},native:nativeDeclaration(provider, this.nativeProviderKeys.get(provider))}))),
        message_renderers: [...this.renderers.keys()].map((customType) => ({ custom_type: customType })),
        entry_renderers: [...this.entryRenderers.keys()].map((customType) => ({ custom_type: customType })),
        markdown_transformer: typeof this.markdownTransformer === "function" || undefined,
        tool_renderers: this.toolRendererResolvers.length || undefined,
        mcp_servers: this.mcpServersPending.size > 0 ? [...this.mcpServersPending].map(([name, config]) => ({ name, config })) : undefined,
        virtual_models: this.virtualModelsPending.length > 0 ? this.virtualModelsPending : undefined,
        unregister_virtual_models: this.virtualModelUnregistrations.length > 0 ? this.virtualModelUnregistrations : undefined,
        // The host sends no session log until an extension asks for it, so the
        // majority which never inspect the session do not each hold a full copy
        // of it resident. The Go, Rust and Python SDKs ask on the first read,
        // which they can do because a read there may block on the host. This
        // runtime exposes getEntries/getBranch synchronously over asynchronous
        // IPC and so has no such moment; it asks here instead, and the host
        // enrols it before building the ready state. Same interface, same
        // answers, different means.
        wants_session_log: true,
        // ctx.modelRegistry's getAll/find/getAvailable answer synchronously
        // from the snapshot the host pushes (ready.models and
        // model_registry_update). The Go, Rust and Python SDKs read
        // getModelRegistryState when asked, so the host builds and sends the
        // catalog only to a runtime that asks for it here.
        wants_model_registry: true,
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
      this.connecting = this.connect();
      await this.connecting;
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
          this.contextValues.cwd = this.ready.cwd || this.contextValues.cwd;
          this.contextValues.mode = this.ready.mode || this.contextValues.mode;
          this.contextValues.model = this.ready.model ? { id: this.ready.model } : this.contextValues.model;
          if (env.ready?.state) {
            // StatePayload is whole here, so an absent frontend is false.
            this.setFrontend(env.ready.state.frontend === true);
            this.applyState(env.ready.state);
          }
          // Pi's interactive mode loads every highlight.js language at
          // startup (interactive-mode.ts); print, JSON and RPC mode keep the
          // eager set.
          if (this.contextValues.mode === "tui") void loadAllHighlightLanguages();
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
            // The descriptor decides, so a guarded getter is not read until the extension reads it (runner.ts:982-988).
            const descriptor = typeof Object.getOwnPropertyDescriptor(this.ctx, key).value === "function" ? { value: this.ctx[key], writable: true } : { get: () => this.ctx[key] };
            Object.defineProperty(requestCtx, key, { enumerable: true, configurable: true, ...descriptor });
          }
          // The request's own cancellation is not ctx.signal, which is the run's. The runtime's internals read it through this key, which no extension can enumerate or collide with.
          Object.defineProperty(requestCtx, requestSignal, { value: controller.signal });
          // Dispatch concurrently so long-running interactive calls
          // (e.g. ui.custom blocking on done()) don't starve incoming
          // notifications such as ui.custom.input. The handler will
          // serialize its response back through the connection on its
          // own when it resolves.
          const quitting = env.request?.method === "event" && env.request.event === "session_shutdown" && env.request.args?.reason === "quit";
          const quitTail = quitDrain.active && env.request?.method === "event" && ["turn_end", "agent_settled"].includes(env.request.event);
          const request = { id: env.id, controller, connection: this.conn, pendingHostCalls: new Set(), settled: false, responded: false, cancelled: false, quit: quitting, quitTail, command: env.request?.method === "command" };
          this.requestRecords.set(env.id, request);
          this.conn?.callEpochs?.set(env.id, 0);
          if (quitting) enterQuitDrain();
          refreshQuitDrain();
          if (PROMPT_END_MODES.has(this.ready?.mode) && this.runSignal === undefined) armTurnWindow(request);
          const task = this.requestContext.run(request, async () => {
            try {
              await this.handleRequest(env.id, env.request || {}, requestCtx);
            } finally {
              request.cancelled ||= !request.settled && controller.signal.aborted;
              request.settled = true;
              this.activeRequests.delete(env.id);
              this.requestRecords.delete(env.id);
              request.connection?.callEpochs?.delete(env.id);
              refreshQuitDrain();
            }
            if (request.responded && endsPrompt(env.request, this.ready?.mode, this.runSignal !== undefined)) awaitHostStep(request.connection);
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
    if (this.widgets.has(key)) this.clearWidget(key);
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
    const { lines, derived } = this.withSurfaceTheme(widget.component, () => {
      const rendered = widget.component.render(width);
      const lines = Array.isArray(rendered) ? rendered.map((line) => String(line)) : [];
      return { lines, derived: this.state.frontend ? this.surfaceView(widget.component, lines, width) : undefined };
    });
    if (width === widget.lastWidth && derived?.key === widget.lastViewKey && lines.length === widget.lastLines.length && lines.every((line, index) => line === widget.lastLines[index])) return;
    widget.lastLines = lines;
    widget.lastWidth = width;
    widget.lastViewKey = derived?.key;
    widget.viewRefs = derived?.images;
    const args = { key, content: lines, options: widget.options, width };
    if (derived) args.view = this.viewWithImages(derived);
    this.fireAndForget("ui.setWidget", args);
  }

  // withSurfaceTheme runs fn, which renders the surface whose root is
  // component, with the root's viewTheme overriding the theme (D107 §13.3),
  // in every mode. An invalid viewTheme is applied nowhere. Components read
  // the runtime's theme or coding-agent's shared one (getSelectListTheme),
  // which another runtime of this process may have installed: both apply it.
  withSurfaceTheme(component, fn) {
    const colors = surfaceTheme(component)?.colors;
    if (!colors) return fn();
    const shared = globalThis[Symbol.for("@earendil-works/pi-coding-agent:theme")];
    const run = shared !== this.ui.theme && shared instanceof ThemeShim ? () => shared.withTokenColors(colors, fn) : fn;
    return this.ui.theme.withTokenColors(colors, run);
  }

  // surfaceView derives the view of component, which just rendered lines at
  // width (D107). key is the encoded view, which dedup compares with the
  // lines; images are the image bytes by ref, sent once per connection.
  surfaceView(component, lines, width, focusRoot = false) {
    let derived;
    try {
      derived = deriveView(component, lines, width, this.ui.theme, { focusRoot, viewTheme: surfaceTheme(component)?.theme });
    } catch {
      return undefined;
    }
    if (!derived) return undefined;
    return { view: derived.view, key: JSON.stringify(derived.view), images: derived.images };
  }

  // viewWithImages is the view to send: the bytes of every image the
  // connection has not sent yet ride along, and count as sent.
  viewWithImages(derived) {
    if (derived.images.size === 0) return derived.view;
    const images = [];
    for (const [ref, image] of derived.images) {
      if (this.viewImageRefs.has(ref)) continue;
      this.viewImageRefs.add(ref);
      images.push({ ref, mimeType: image.mimeType, data: image.data });
    }
    return images.length > 0 ? { ...derived.view, images } : derived.view;
  }

  // rendererResult is a render_* result: the lines, and the component's view
  // while a frontend draws (D107).
  rendererResult(component, lines, width) {
    const result = { lines };
    if (!this.state.frontend || !component || Array.isArray(component) || typeof component.render !== "function") return result;
    const derived = this.surfaceView(component, lines, Number(width) || 0);
    if (derived) result.view = this.viewWithImages(derived);
    return result;
  }

  // setFrontend applies StatePayload.frontend. A change re-renders the
  // dedup'd surfaces, so a frontend gets their structure without waiting for
  // their next change; header and footer re-render with every state.
  setFrontend(frontend) {
    if (this.state.frontend === frontend) return;
    this.state.frontend = frontend;
    for (const key of this.widgets.keys()) this.requestWidgetRender(key);
    for (const overlay of this.customOverlays?.values() || []) overlay.renderFrame();
  }

  // evictViewImages forgets refs whose bytes the host dropped
  // (ui.view.evicted), and sends again every surface whose last view named
  // one, now with the data.
  evictViewImages(refs) {
    if (!Array.isArray(refs)) return;
    const evicted = new Set(refs.filter((ref) => this.viewImageRefs.delete(ref)));
    if (evicted.size === 0) return;
    const named = (images) => images !== undefined && [...images.keys()].some((ref) => evicted.has(ref));
    for (const [key, widget] of this.widgets) {
      if (!named(widget.viewRefs)) continue;
      widget.lastViewKey = undefined;
      this.requestWidgetRender(key);
    }
    for (const overlay of this.customOverlays?.values() || []) {
      if (named(overlay.viewRefs)) overlay.resendFrame();
    }
    for (const kind of ["header", "footer"]) {
      if (named(this.specialSurfaceComponents.get(kind)?.viewRefs)) this.renderSpecialSurface(kind);
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
        this.contextValues.sessionManager._cachedIndex = undefined;
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
    // Settings are absent until the Host has some (StatePayload).
    if (Object.hasOwn(snapshot, "settings")) this.state.settings = snapshot.settings;
    // Pi's TUI and its extensions share one capability cache; here the host
    // owns the terminal, so its resolved capabilities seed the cache pi-tui's
    // Markdown reads.
    // The host's active theme colors ctx.ui.theme and the pi-coding-agent
    // theme helpers, as Pi's global theme does in its own process.
    if (snapshot.theme && typeof snapshot.theme === "object") this.ui.theme.setPalette(snapshot.theme);
    applyTerminalCapabilities(snapshot.terminalCapabilities);
    if (snapshot.keybindings && typeof snapshot.keybindings === "object") this.applyKeybindings(snapshot.keybindings);
    if (typeof snapshot.hasUI === "boolean") this.state.hasUI = snapshot.hasUI;
    this.contextValues.model = normalizeModel(this.state.model);
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

  renderSpecialSurface(kind, replace = false) {
    if (!this.conn) return;
    const factory = kind === "footer" ? this.footerFactory : this.headerFactory;
    if (!factory) return;
    try {
      // Reuse the component so renders do not repeat factory side effects or subscriptions.
      let surface = this.specialSurfaceComponents.get(kind);
      if (!surface) {
        const tuiShim = this.specialSurfaceTui(kind);
        const component = kind === "footer"
          ? factory(tuiShim, this.ui.theme, this.footerDataProvider())
          : factory(tuiShim, this.ui.theme);
        surface = { component, id: ++this.specialSurfaceSeq };
        this.specialSurfaceComponents.set(kind, surface);
      }
      const width = this.ready.width || 80;
      const { lines, derived } = this.withSurfaceTheme(surface.component, () => {
        const rendered = surface.component?.render?.(width);
        const lines = Array.isArray(rendered) ? rendered.map((v) => String(v)) : [];
        return { lines, derived: this.state.frontend ? this.surfaceView(surface.component, lines, width) : undefined };
      });
      const args = {
        clear: false,
        lines,
        width,
        surfaceId: surface.id,
        updateOnly: !replace,
      };
      surface.viewRefs = derived?.images;
      if (derived) args.view = this.viewWithImages(derived);
      this.fireAndForget(kind === "footer" ? "ui.setFooter" : "ui.setHeader", args);
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
      resendFrame: () => {},
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
    let lastViewKey;
    let lastMouse = false;
    // ui.view.evicted: the next frame goes out with the image data again.
    overlay.resendFrame = () => {
      lastViewKey = undefined;
      overlay.renderFrame();
    };
    const renderNow = () => {
      if (!overlay.active || doneCalled || !this.customOverlays.has(key)) return;
      // termWidth tags the frame's terminal geometry so the host can drop
      // frames laid out for a stale terminal width; the component itself is
      // rendered at the resolved overlay width.
      const termWidth = this.ready.width || 80;
      const width = renderWidth(termWidth);
      let out;
      let derived;
      try {
        ({ out, derived } = this.withSurfaceTheme(component, () => {
          const lines = component.render?.(width);
          const out = Array.isArray(lines) ? lines.map((value) => String(value)) : [];
          return { out, derived: this.state.frontend ? this.surfaceView(component, out, width, true) : undefined };
        }));
      } catch (err) {
        closeWithError(new Error(`ui.custom render failed: ${err?.message || String(err)}`));
        return;
      }
      // The frame says whether the component takes the
      // mouse, so the host hands it fullscreen mouse events.
      const mouse = takesMouse(component);
      if (termWidth === lastWidth && mouse === lastMouse && derived?.key === lastViewKey && out.length === lastLines.length && out.every((value, index) => value === lastLines[index])) return;
      lastLines = out;
      lastWidth = termWidth;
      lastViewKey = derived?.key;
      lastMouse = mouse;
      overlay.viewRefs = derived?.images;
      overlay.seq += 1;
      try {
        const args = { key, lines: out, width: termWidth, seq: overlay.seq };
        if (derived) args.view = this.viewWithImages(derived);
        if (mouse) args.mouse = true;
        this.notify("ui.custom.render", args);
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
    return this.modelFor(modelRegistryKey(provider, modelId));
  }

  // modelFor returns this extension's copy of a catalog model, made the first time it reads that model.
  modelFor(key) {
    let model = this.models.get(key);
    if (model !== undefined || this.modelsComplete) return model;
    const template = this.catalog.get(key);
    if (template === undefined) return undefined;
    model = structuredClone(template);
    this.models.set(key, model);
    return model;
  }

  // allModels returns this extension's copies of every catalog model, in catalog order, keeping each copy it has already read.
  allModels() {
    if (!this.modelsComplete) {
      const models = new Map();
      for (const [key, template] of this.catalog) models.set(key, this.models.get(key) ?? structuredClone(template));
      this.models = models;
      this.modelsComplete = true;
    }
    return this.models;
  }

  hasModelsOf(provider) {
    for (const model of this.modelsComplete ? this.models.values() : this.catalog.values()) {
      if (model.provider === provider) return true;
    }
    return false;
  }

  // applyModelRegistryState installs a host registry snapshot: the catalog
  // and the provider, error and registration state ctx.modelRegistry reads.
  // The members of a cell share the decoded snapshot, so each takes its own
  // copy of what an extension can reach.
  applyModelRegistryState(state) {
    if (!state || typeof state !== "object") return;
    if (Array.isArray(state.models)) this.replaceModels(state.models);
    this.registryProviders.clear();
    this.registryState = {
      typedModels: Array.isArray(state.typedModels) ? state.typedModels.map((model) => ({ ...structuredClone(model) })) : [],
      providers: state.providers && typeof state.providers === "object" ? structuredClone(state.providers) : {},
      registered: Array.isArray(state.registered) ? structuredClone(state.registered) : [],
      error: typeof state.error === "string" && state.error !== "" ? state.error : undefined,
    };
  }

  // Compose the same built-in, models.json and extension layers as Pi. Auth
  // methods receive the caller's AuthContext; they do not capture host secrets.
  getRegistryProvider(id) {
    const native = this.contextValues.modelRegistry.getRegisteredNativeProvider(id);
    if (native) return native;
    const state = this.registryState.providers?.[id];
    const hasModels = this.hasModelsOf(id);
    const config = this.registeredProviderConfig(id) ?? this.registryState.registered?.find(entry => entry.name === id)?.config ?? state?.extensionConfig;
    // A registration held here is a provider even when the snapshot and the model list have none yet.
    if (!state && !hasModels && config === undefined) return undefined;
    // The snapshot predates a registration this process made since, and Pi recomposes inside registerProvider (model-runtime.ts:919-940), so a registration held here makes the provider composed whatever the snapshot says.
    if (!state?.composed && config === undefined) {
      const builtin = piBuiltinProvider(id);
      if (builtin) return builtin;
    }
    // A cell member's re-registration replaces the shared root; Pi recomposes on registration, so a composition is reused only for the root it was built from.
    const cached = this.registryProviders.get(id);
    if (cached && cached.config === config) return cached.provider;
    const provider = piProviderComposer().composeModelProvider(id, piBuiltinProvider(id), { getProvider: () => state?.modelsConfig }, config);
    this.registryProviders.set(id, { config, provider });
    return provider;
  }

  replaceModels(models) {
    this.catalog = catalogOf(models);
    this.models = new Map();
    this.modelsComplete = this.catalog.size === 0;
  }

  // The signal of a tool's execute(). Pi hands every tool the run's signal, which is also ctx.signal (agent-loop.ts, runner.ts:917-920), except a nested call whose caller passed options.signal: that call runs with it (runner.ts:979-981), which this runtime holds when the caller is one of its own extensions (the call's executeId) and otherwise sees as the request's own cancellation. Without a run, as when a host executes a tool outside one, the request's cancellation is all there is.
  toolSignal(request, ctx) {
    if (request.own_signal) return this.nestedCalls.get(request.execute_id)?.signal ?? ctx[requestSignal];
    return this.runSignal ? this.runSignal.controller.signal : ctx[requestSignal];
  }

  // The Host's run_signal frame (run_signal.go): a run in progress has one AbortSignal for its whole life, aborted with the run, and none exists while no run is active (runner.ts:917-920, agent.ts:336-338).
  applyRunSignal({ run, active, aborted }) {
    if (!active) {
      this.runSignal = undefined;
      return;
    }
    if (this.runSignal?.id !== run) this.runSignal = { id: run, controller: new AbortController() };
    if (aborted) this.runSignal.controller.abort();
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
      case "bash_operations_release": {
        let args = notify.args;
        try {
          if (typeof args === "string") args = JSON.parse(args);
        } catch {
          return;
        }
        this.bashOperations.delete(args?.handle);
        return;
      }
      case "run_signal": {
        let args = notify.args;
        try {
          if (typeof args === "string") args = JSON.parse(args);
        } catch {
          return;
        }
        this.applyRunSignal(args ?? {});
        return;
      }
      case "invalidate": {
        // runner.ts:679-690: the outgoing Session's runner is invalidated, so a captured pi or ctx throws its stale message.
        let args = notify.args;
        try {
          if (typeof args === "string") args = JSON.parse(args);
        } catch {
          args = {};
        }
        this.extensionRuntime.invalidate(args?.message || undefined);
        return;
      }
      case "runtime_input_end":
        quitDrain.inputEnded = true;
        releaseCommandSuspensions();
        listenForQuitDrain();
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
      case "ui.surface_retired": {
        const args = typeof notify.args === "string" ? JSON.parse(notify.args) : notify.args;
        const surface = this.specialSurfaceComponents.get(args?.kind);
        if (!surface || surface.id !== args?.surfaceId) return;
        this.specialSurfaceComponents.delete(args.kind);
        if (args.kind === "footer") this.footerFactory = undefined;
        else this.headerFactory = undefined;
        try {
          surface.component?.dispose?.();
        } catch (err) {
          this.fireAndForget("ui.notify", { message: `${args.kind} dispose failed: ${err?.message || String(err)}`, level: "error" });
        }
        return;
      }
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
          const state = args?.state ?? args;
          if (state && typeof state === "object") this.setFrontend(state.frontend === true);
          this.applyState(state);
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
          for (const key of this.widgets.keys()) this.renderWidget(key);
          for (const overlay of this.customOverlays?.values() || []) {
            overlay.renderFrame();
          }
          this.renderSpecialSurface("header");
          this.renderSpecialSurface("footer");
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
      case "ui.custom.mouse": {
        // A fullscreen mouse event inside the
        // component's bounds, as Pi's renderer hands it to handleMouse.
        let args = notify.args;
        try {
          if (typeof args === "string") args = JSON.parse(args);
        } catch {
          args = {};
        }
        const overlay = this.customOverlays?.get(args?.key);
        if (!overlay?.component || !args?.event) return;
        try {
          const result = dispatchOverlayMouse(overlay, args.event);
          // Pi's renderer draws again as the result asks, by default after
          // press, click, drag and wheel (applyMouseDispatchResult).
          if (result?.render ?? !["move", "release"].includes(args.event.type)) overlay.renderImmediate();
        } catch (err) {
          overlay.reject?.(err instanceof Error ? err : new Error(String(err)));
        }
        return;
      }
      case "ui.view.evicted": {
        // pig additive (D107): the host dropped these image bytes.
        try {
          const args = typeof notify.args === "string" ? JSON.parse(notify.args) : notify.args;
          this.evictViewImages(args?.refs);
        } catch {}
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
    // Pi merges the defined values of a re-registration over the previous root (model-runtime.ts:753-766), so a registration that defines neither keeps the implementations of the root it merged over.
    if (config.images !== undefined || config.classifiers !== undefined) {
      this.providerOperations.set(name, { images: config.images, classifiers: config.classifiers });
      const {images, classifiers, ...serializable} = config;
      config = serializable;
    } else this.providerOperations.delete(name);
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
    if (this.providersSent) this.fireAndForget("registerProvider", { ...this.providerDeclaration(name, config), configRef: this.providerConfigRef(merged) });
  }

  // The declaration of a provider config, as the register frame and a registration after the factory carry it: the config without its callbacks, and the operations that run in this process.
  providerDeclaration(name, config) {
    return { name, config, ...(this.providerStreams.has(name) ? { stream_simple: true } : {}), ...this.providerOperationAPIs(name) };
  }

  // The APIs a provider config implements, as the register frame names them.
  providerOperationAPIs(name) {
    const operations = this.providerOperations.get(name);
    const apis = (implementations, method) => Object.entries(implementations ?? {}).filter(([, implementation]) => typeof implementation?.[method] === "function").map(([api]) => api).sort();
    const images = apis(operations?.images, "generateImages");
    const classifiers = apis(operations?.classifiers, "classify");
    return { ...(images.length > 0 ? { image_apis: images } : {}), ...(classifiers.length > 0 ? { classifier_apis: classifiers } : {}) };
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
    this.providerOperations.delete(provider.id);
    this.providers.delete(provider.id);
    this.nativeProviders.set(provider.id, provider);
    if (this.loadState === "active") {
      this.transferProviderOwnership(provider.id, undefined);
      this.nativeProviderObjects.set(provider.id, { owner: this, provider });
    }
    const models = this.allModels();
    for(const [key,model] of models) if(model.provider===provider.id)models.delete(key);
    for(const model of declaration.models) models.set(modelRegistryKey(provider.id,model.id),normalizeModel(model));
    if(this.providersSent) this.fireAndForget("registerProvider",{name:provider.id,config:{},native:declaration});
  }

  unregisterProvider(name) {
    this.registryProviders.delete(name);
    this.providers.delete(name);
    this.providerStreams.delete(name);
    this.providerOperations.delete(name);
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

  // Pi's unregisterProvider recomposes the provider from its built-in and models.json layers before it returns (model-runtime.ts:942-948), so the snapshot's extension layer goes with the registration.
  dropRegistration(name) {
    const providers = this.registryState.providers ?? {};
    const state = providers[name];
    let next = providers;
    if (state?.extensionConfig !== undefined) {
      const { extensionConfig: _dropped, ...rest } = state;
      next = { ...providers, [name]: { ...rest, composed: rest.modelsConfig !== undefined } };
    }
    this.registryProviders.delete(name);
    this.registryState = { ...this.registryState, providers: next, registered: (this.registryState.registered ?? []).filter((entry) => entry.name !== name) };
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
        // The Host sends signal_timeout_ms for a stored-credential refresh, whose signal is AbortSignal.timeout alone (the Host never cancels it for the caller); any other refresh gets the request's cancellation.
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
          await this.respond(id, await dispatchFacet(this, request, { signal: ctx[requestSignal] }));
          return;
        }
        case "provider_object_callback": {
          const result = await dispatchProviderObjectCallback(this, request);
          await this.respond(id, result);
          return;
        }
        case "provider_call":
        case "provider_stream": {
          const result=await dispatchNativeProvider(this,id,request,{ signal: ctx[requestSignal] });
          await this.respond(id,result);
          return;
        }
        case "provider_stream_simple": {
          const callback = this.providerStreams.get(request.tool);
          if (!callback) throw new Error(`unknown provider stream: ${request.tool}`);
          const {model, context, options} = request.args;
          // Pi's composer calls extension.streamSimple with the effective registered root as its receiver (provider-composer.ts:500-501).
          const stream = await callback.call(this.registeredProviderConfig(request.tool), model, context, {...options, signal:ctx[requestSignal]});
          for await (const event of stream) {
            if (ctx[requestSignal].aborted) throw new Error("provider stream aborted");
            const accepted = this.conn.send({type:"notify", notify:{method:"provider_stream_event",args:{request_id:id,result:event}}});
            if (!accepted) await new Promise((resolve,reject)=>{
              const finish=(error)=>{this.conn.socket.off("drain",drain);this.conn.socket.off("close",closed);ctx[requestSignal].removeEventListener("abort",aborted);error?reject(error):resolve();};
              const drain=()=>finish();const closed=()=>finish(new Error("extension connection closed"));const aborted=()=>finish(new Error("provider stream aborted"));
              this.conn.socket.once("drain",drain);this.conn.socket.once("close",closed);ctx[requestSignal].addEventListener("abort",aborted,{once:true});if(ctx[requestSignal].aborted)aborted();
            });
          }
          await this.respond(id, await stream.result());
          return;
        }
        case "user_bash_exec": {
          // types.ts BashOperations.exec: the object the user_bash reply named runs the command; onData chunks stream before the answer and the request's cancellation is the signal.
          const operations = this.bashOperations.get(request.tool);
          if (typeof operations?.exec !== "function") throw new Error(`unknown bash operations: ${request.tool}`);
          const { command, cwd, timeout, env } = request.args ?? {};
          let done = false;
          const onData = (data) => {
            if (done) return;
            const bytes = typeof data === "string" ? Buffer.from(data) : Buffer.from(data);
            this.notify("tool_update", { request_id: id, result: { data: bytes.toString("base64") } });
          };
          let result;
          try {
            result = await operations.exec(command, cwd, { onData, signal: ctx[requestSignal], ...(timeout === undefined ? {} : { timeout }), ...(env === undefined ? {} : { env }) });
          } finally {
            done = true;
          }
          await this.respond(id, { exitCode: result?.exitCode ?? null });
          return;
        }
        case "provider_operation": {
          // types.ts:1896-1898: the implementation of the config's image or classifier API runs here with the model, the context and the resolved request options; its result, or its error, is the answer.
          const { kind, api, model, context, options } = request.args ?? {};
          if (kind !== "images" && kind !== "classifiers") throw new Error(`Unknown provider operation ${JSON.stringify(kind)}`);
          const label = kind === "images" ? "image" : "classifier";
          const method = kind === "images" ? "generateImages" : "classify";
          const implementation = this.providerOperations.get(request.tool)?.[kind]?.[api];
          if (typeof implementation?.[method] !== "function") throw new Error(`Provider ${request.tool} has no ${label} implementation for ${JSON.stringify(api)}`);
          await this.respond(id, await implementation[method](model, context, { ...options, signal: ctx[requestSignal] }));
          return;
        }
        case "tool_call": {
          const tool = this.tools.get(request.tool);
          if (!tool) throw new Error(`unknown tool: ${request.tool}`);
          // agent-loop.ts:707-716: the Host ran prepareArguments (tool_prepare_arguments) before it validated, so these arguments are the prepared ones.
          const params = request.args || {};
          // Upstream passes a live AbortSignal and an onUpdate that streams
          // partial results; both end with this request.
          let done = false;
          const onUpdate = (partial) => {
            if (!done) this.notify("tool_update", { request_id: id, result: this.normalizeToolResult(partial) });
          };
          let result;
          try {
            result = await tool.execute?.(request.tool_call_id, params, this.toolSignal(request, ctx), onUpdate, this.addToolContext(ctx, request.tool_call_id));
          } finally {
            done = true;
          }
          await this.respond(id, this.normalizeToolResult(result));
          return;
        }
        case "tool_prepare_arguments": {
          // agent-loop.ts:707-716: prepareToolCall runs the synchronous hook on the model's arguments, then validates what it returns.
          const tool = this.tools.get(request.tool);
          if (typeof tool?.prepareArguments !== "function") throw new Error(`tool ${request.tool} has no prepareArguments`);
          await this.respond(id, tool.prepareArguments(request.args || {}));
          return;
        }
        case "tool_prepare_loadout": {
          // agent-session.ts:1514-1519: the hook is synchronous and answers from the loadout the session built.
          const tool = this.tools.get(request.tool);
          if (typeof tool?.prepareLoadout !== "function") throw new Error(`tool ${request.tool} has no prepareLoadout`);
          const payload = request.args ?? {};
          const exposures = payload.exposures ?? {};
          const namespaces = payload.namespaces ?? {};
          const promptGuidelines = payload.promptGuidelines ?? {};
          const loadout = {
            declared: payload.declared ?? [],
            callable: payload.callable ?? [],
            registered: payload.registered ?? [],
            // A tool without a definition is "direct" (agent-session.ts:1480-1482).
            getExposure: (name) => (Object.hasOwn(exposures, name) ? exposures[name] : "direct"),
            getNamespace: (name) => (Object.hasOwn(namespaces, name) ? namespaces[name] : undefined),
            // A tool without guidelines has none (agent-session.ts getPromptGuidelines).
            getPromptGuidelines: (name) => (Object.hasOwn(promptGuidelines, name) ? promptGuidelines[name] : []),
          };
          await this.respond(id, tool.prepareLoadout(loadout) ?? null);
          return;
        }
        case "execute_tool_update": {
          // The Host waits for each partial result of a nested call, in order and before the outcome. Pi calls onUpdate for every partial result, even after one threw; the throw skips that update's event and rejects the call with the first error after the tool returned (nested-tool-calls.ts:219-231, agent-loop.ts:833-845). The answer tells the Host.
          const nested = this.nestedCalls.get(request.args?.executeId);
          if (nested?.onUpdate === undefined) { await this.respond(id, null); return; }
          try { nested.onUpdate(request.args.result); }
          catch (error) {
            if (!nested.hasFailure) { nested.hasFailure = true; nested.failure = error; }
            await this.respond(id, null, error);
            return;
          }
          await this.respond(id, null);
          return;
        }
        case "virtual_model_route": {
          // loader.ts:485-487: the route runs with the request and a fresh context, after the runner bound.
          const { provider, id: modelId, request: routeRequest } = request.args ?? {};
          const model = this.virtualModels.get(JSON.stringify([provider, modelId]));
          if (!model) throw new Error(`unknown virtual model ${provider}/${modelId}`);
          const routed = await model.route({
            ...routeRequest,
            model: normalizeModel(routeRequest.model),
            ...(routeRequest.previous ? { previous: { ...routeRequest.previous, model: normalizeModel(routeRequest.previous.model) } } : {}),
            ...(routeRequest.failed ? { failed: { ...routeRequest.failed, model: normalizeModel(routeRequest.failed.model) } } : {}),
            signal: ctx[requestSignal],
          }, ctx);
          // agent-session.ts:788 keeps the state when `route.state === request.state`. Identity is observable here and not across the process boundary, so the reply says it.
          await this.respond(id, { model: routed.model, thinkingLevel: routed.thinkingLevel, state: routed.state, ...(routed.state !== undefined && routed.state === routeRequest.state ? { stateUnchanged: true } : {}) });
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
            result = await fn(value, owned.model, ctx[requestSignal]);
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
        case "with_session": {
          // agent-session-runtime.ts:187-194: the callback runs with the replacement Session's context and its settlement settles the replacement call.
          const { handle, ready } = request.args ?? {};
          const entry = this.withSessionCallbacks.get(handle);
          if (!entry) throw new Error(`unknown withSession callback ${handle}`);
          try {
            await entry.callback(this.createReplacedSessionContext(ready ?? {}, id));
          } catch (error) {
            entry.failed = true;
            entry.error = error;
            throw error;
          }
          await this.respond(id, null);
          return;
        }
        case "setup": {
          // agent-session-runtime.ts:254-257: the callback seeds the replacement Session through its SessionManager and its settlement settles the replacement call.
          const { handle } = request.args ?? {};
          const entry = this.setupCallbacks.get(handle);
          if (!entry) throw new Error(`unknown setup callback ${handle}`);
          const owner = this.requestContext.getStore();
          const parent = () => (owner?.id === id && !owner.settled ? id : this.activeRequest()?.id ?? "");
          try {
            await entry.callback(new SetupSessionManager(this, parent));
          } catch (error) {
            entry.failed = true;
            entry.error = error;
            throw error;
          }
          await this.respond(id, null);
          return;
        }
        case "command": {
          const cmd = this.commands.get(request.tool);
          if (!cmd) throw new Error(`unknown command: ${request.tool}`);
          const owner = this.requestContext.getStore();
          if (owner?.id === id) owner.command = true;
          if (owner?.id === id && this.contextValues.mode === "rpc") armRequestWindow(owner, true);
          const pending = cmd.handler?.(request.args ?? "", ctx);
          if (this.contextValues.mode === "rpc") await this.acknowledgeInvocation(id, pending);
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
            event.signal = ctx[requestSignal];
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
              const pending = selected.handler(event, ctx);
              if (request.event === "agent_before_settle") this.armSettleTailWindow(id, ctx);
              result = await pending;
            } catch (err) {
              error = err instanceof Error ? err : new Error(String(err));
            }
            await this.respond(id, { _pigBoundaryEntries: entries, _pigBoundaryResult: result }, error);
            return;
          }
          // Pi hands every tool_call handler the one event object, and a handler edits event.input in place (runner.ts emitToolCall). The host cannot share the object, so the reply carries the input the handler left, when it differs from the one it received, even if the handler failed. The tool runs with the object the event carried (agent-loop.ts prepareToolCall passes the same args to beforeToolCall and execute), so a handler that assigns a new object to event.input changes nothing the tool receives.
          if (request.event === "tool_call") {
            const input = event.input;
            const before = JSON.stringify(input);
            let result;
            let error;
            try {
              result = await selected.handler(event, ctx);
            } catch (err) {
              error = err instanceof Error ? err : new Error(String(err));
            }
            const edited = JSON.stringify(input) !== before;
            await this.respond(id, { ...(edited ? { _pigToolCallInput: input } : {}), _pigToolCallResult: result }, error);
            return;
          }
          if (request.event === "before_agent_start") {
            const options = event.systemPromptOptions;
            // Pi's normalized options always carry every collection; the host omits an empty one. A handler's own null stays.
            for (const [name, empty] of [["selectedTools", () => []], ["hiddenTools", () => []], ["toolSnippets", () => ({})], ["toolGuidelines", () => ({})], ["promptGuidelines", () => []], ["contextFiles", () => []], ["skills", () => []]]) {
              if (options[name] === undefined) options[name] = empty();
            }
            let result;
            let error;
            try {
              result = await selected.handler(event, ctx);
            } catch (err) {
              error = err instanceof Error ? err : new Error(String(err));
            }
            await this.respond(id, { _pigPromptSections: options.sections, _pigPromptSelectedTools: options.selectedTools, _pigPromptOptions: options, _pigPromptResult: result }, error);
            return;
          }
          const messages = event.messages;
          const snapshot = Array.isArray(messages) ? messages.slice() : undefined;
          const pending = selected.handler(event, ctx);
          const shutdownOwner = this.requestContext.getStore();
          if (shutdownOwner?.id === id && shutdownOwner.quit && this.contextValues.mode === "rpc") armRequestWindow(shutdownOwner);
          if (request.event === "agent_settled") this.armSettleTailWindow(id, ctx);
          // Calling an async handler executes its synchronous prefix before returning its Promise. Unawaited notifications admit the next caller at that boundary.
          if (request.event === "ui_prompt_start" || request.event === "ui_prompt_end" || request.event === "session_info_changed") {
            this.conn.requestState(id, "blocked", "external_io");
          }
          if (request.event === "session_shutdown" && this.contextValues.mode === "rpc") await this.acknowledgeInvocation(id, pending);
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
            // pig additive (D19): functions cannot cross the socket, so the reply names the operations object and the host calls it with user_bash_exec.
            if (typeof result === "object" && result.operations !== undefined && result.result === undefined && typeof result.operations?.exec === "function") {
              const handle = `bash-${++this.bashOperationsSeq}`;
              this.bashOperations.set(handle, result.operations);
              result = { operations: { handle } };
            }
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
          await this.respond(id, this.withSurfaceTheme(component, () => {
            const lines = Array.isArray(component) ? component : component?.render?.(payload.width);
            return this.rendererResult(component, Array.isArray(lines) ? lines : [], payload.width);
          }));
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
          await this.respond(id, this.withSurfaceTheme(component, () => {
            const lines = Array.isArray(component) ? component : component?.render?.(payload.width);
            return this.rendererResult(component, Array.isArray(lines) ? lines : [], payload.width);
          }));
          return;
        }
        case "resolve_tool_renderers": {
          await this.respond(id, this.resolveToolRenderers(request.args || {}));
          return;
        }
        case "render_tool": {
          const payload = request.args || {};
          const tool = payload.renderers ? this.resolvedToolRenderers.get(payload.renderers) : this.tools.get(request.tool);
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
          await this.respond(id, this.withSurfaceTheme(component, () => {
            const lines = component?.render?.(payload.width);
            return this.rendererResult(component, Array.isArray(lines) ? lines : [], payload.width);
          }));
          return;
        }
        case "autocomplete.suggest": {
          await this.respond(id, await this.autocomplete.suggest(request.args, ctx[requestSignal]));
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
          await this.dispatchOAuth(id, request, ctx[requestSignal]);
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
    }
    this.conn.respond(id, result, error);
  }

  // The host request whose handler is running the current code. Code the
  // handler left scheduled when it returned (a timer, a promise it did not
  // await) still carries the request's async context, but it is no longer part
  // of that request. As in Pi, where nothing ties a call to the event that
  // scheduled it, the command's return cancels none of its host calls, and
  // calls made later are the extension's own: pi-powerline-footer opens its welcome overlay from a
  // timer its session_start handler starts.
  activeRequest() {
    const request = this.requestContext.getStore();
    return request && !request.settled ? request : undefined;
  }

  normalizeToolResult(result) {
    if (result == null) return {};
    if (typeof result === "string") return { content: result };
    if (typeof result !== "object") return { content: String(result) };
    // Pi hands the tool's own object on, so the events and the JSON stream write its members in the order the tool wrote them (agent-loop.ts:778-786, 912-919): the wire object lists the members the tool set first, in its order, then the rest.
    const members = {
      content: () => toolContent(result.content ?? result),
      details: () => result.details,
      is_error: () => Boolean(result.isError ?? result.is_error),
      structured_content: () => result.structuredContent,
      terminate: () => (typeof result.terminate === "boolean" ? result.terminate : undefined),
      usage: () => result.usage,
    };
    const wireNames = { isError: "is_error", structuredContent: "structured_content" };
    // isError is sent only when the tool wrote it: a false one the tool did not write would be a member of the object that Pi's has not.
    const own = Object.keys(result).map((name) => wireNames[name] ?? name);
    const wire = {};
    for (const key of [...own, ...Object.keys(members).filter((name) => name !== "is_error")]) {
      if (Object.hasOwn(members, key) && !Object.hasOwn(wire, key)) wire[key] = members[key]();
    }
    return wire;
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
    if (!facetMethod && !themeMethod && !["registerTool", "registerMcpServer", "unregisterMcpServer", "registerVirtualModel", "unregisterVirtualModel", "mcpServers.check", "getMcpServers", "getCallableTools", "getAllTools", "getActiveTools", "xref.hello", "xref.op", "provider.config", "events.on", "events.off", "events.emit", "events.settle"].includes(method) && method !== "ui.custom.control" && method !== "provider.retain" && method !== "provider.release" && !providerMethod && !providerCallback) {
      throw new Error(`Host method ${method} is not a synchronous bridge operation`);
    }
    if (!this.conn) throw new Error("runtime not connected");
    const parent = this.activeRequest()?.id ?? "";
    if (parent) this.conn.requestState(parent, "blocked", "host_call");
    try { return this.conn.callSync(method, args, parent); }
    finally { if (parent) this.conn.requestState(parent, "progress"); }
  }

  // A detached call is not tied to the request that made it: the host does not cancel it with that request. The request still reports it as blocked while it waits.
  async call(method, args = {}, { detached = false, parent = undefined } = {}) {
    // A callback the Host declared for one request is owned by that request's record, not
    // by whatever request (if any) is ambient where the extension invokes it.
    const ambient = this.requestContext.getStore();
    const owner = parent ? this.requestRecords.get(parent) : ambient;
    // While the factory runs the runtime holds only its early connection, opened by its first host call; Pi's factory can already await pi.exec().
    if (!owner && !this.connection()) this.ensureConnectionSync();
    const current = this.connection();
    const connection = owner?.connection ?? ambient?.connection ?? current;
    if (!connection || connection.closed || connection !== current) throw new Error("extension connection closed or replaced");
    if (!detached && (owner?.cancelled || (!owner?.settled && owner?.controller.signal.aborted))) {
      throw hostCancelled("host call cancelled with its parent request");
    }
    // A callback the Host declared for one request belongs to that request even when
    // the extension invokes it while handling another one, so the caller can bind it.
    const reportedRequestId = parent ?? (owner && !owner.settled ? owner.id : "");
    const parentRequestId = detached ? "" : reportedRequestId;
    if (reportedRequestId && !USER_BLOCKING_CALLS.has(method)) {
      connection.requestState(reportedRequestId, "blocked", "host_call");
    }
    try {
      return await connection.call(method, args, parentRequestId);
    } finally {
      if (reportedRequestId && !owner?.responded && !connection.closed) connection.requestState(reportedRequestId, "progress");
    }
  }

  // A withSession callback (types.ts:410-430) cannot cross the process boundary: the call names it by handle, and the host runs it with a with_session request before the call settles. A callback that throws rejects the call with its own error, as Pi's awaited callback does.
  async callReplacement(method, args, withSession) {
    if (typeof withSession !== "function") return this.call(method, args);
    const handle = `${this.name}:${++this.nextWithSessionHandle}`;
    const entry = { callback: withSession, failed: false, error: undefined };
    this.withSessionCallbacks.set(handle, entry);
    try {
      return await this.call(method, { ...args, withSession: handle });
    } catch (error) {
      throw entry.failed ? entry.error : error;
    } finally {
      this.withSessionCallbacks.delete(handle);
    }
  }

  // callNewSession is newSession with Pi's setup option (types.ts:411): the callback cannot cross the process boundary, so the call names it by handle and the host runs it with a setup request after the replacement Session is bound and before withSession. A callback that throws rejects the call with its own error.
  async callNewSession(args, setup, withSession) {
    if (typeof setup !== "function") return this.callReplacement("newSession", args, withSession);
    const handle = `${this.name}:setup:${++this.nextSetupHandle}`;
    const entry = { callback: setup, failed: false, error: undefined };
    this.setupCallbacks.set(handle, entry);
    try {
      return await this.callReplacement("newSession", { ...args, setup: handle }, withSession);
    } catch (error) {
      throw entry.failed ? entry.error : error;
    } finally {
      this.setupCallbacks.delete(handle);
    }
  }

  // createReplacedSessionContext is Pi's createReplacedSessionContext (agent-session.ts:4325-4333) for the with_session request requestId: a command context of the replacement Session, read from the ready payload the host sent for it, with awaited sendMessage and sendUserMessage. Its host calls belong to the request, so the host applies them to the replacement Session.
  createReplacedSessionContext(ready, requestId) {
    const runtime = Object.create(this);
    runtime.extensionRuntime = { assertActive() {} };
    runtime.ready = { ...this.ready, cwd: ready.cwd || this.ready.cwd, mode: ready.mode || this.ready.mode, model: ready.model };
    const snapshot = ready.state ?? {};
    const session = snapshot.session ?? {};
    runtime.state = {
      ...this.state,
      activeTools: [...(snapshot.activeTools ?? [])],
      allTools: [...(snapshot.allTools ?? [])],
      commands: [...(snapshot.commands ?? [])],
      thinkingLevel: typeof snapshot.thinkingLevel === "string" ? snapshot.thinkingLevel : this.state.thinkingLevel,
      scopedModels: Array.isArray(snapshot.scopedModels) ? snapshot.scopedModels : [],
      model: normalizeModel(snapshot.model),
      isIdle: snapshot.isIdle !== false,
      projectTrusted: snapshot.projectTrusted === true,
      hasPendingMessages: snapshot.hasPendingMessages === true,
      contextUsage: snapshot.contextUsage == null ? undefined : { ...snapshot.contextUsage },
      systemPrompt: typeof snapshot.systemPrompt === "string" ? snapshot.systemPrompt : "",
      systemPromptOptions: snapshot.systemPromptOptions ?? this.state.systemPromptOptions,
      flags: { ...(snapshot.flags ?? {}) },
      hasUI: snapshot.hasUI === true,
      session: { sessionId: session.sessionId, sessionName: session.sessionName, sessionFile: session.sessionFile, leafId: session.leafId, info: session.info, entries: [] },
    };
    const context = new RuntimeContext(runtime);
    // The host drops without an answer a call whose parent request has ended, so a synchronous read that named the settled with_session request would block this process for good. Once the callback settles, a read of a context the extension kept goes out like any other call of the code that runs it.
    const owner = this.requestContext.getStore();
    const parent = () => (owner?.id === requestId && !owner.settled ? requestId : this.activeRequest()?.id ?? "");
    runtime.contextValues.sessionManager = new ReplacedSessionManager(this, parent);
    // The replacement Session's live values come from its host; this runtime replicates only the Session that requested the replacement.
    const read = (method) => this.conn.callSync(method, {}, parent());
    context.isIdle = () => read("isIdle")?.idle === true;
    context.isProjectTrusted = () => read("isProjectTrusted")?.trusted === true;
    context.hasPendingMessages = () => read("hasPendingMessages")?.pending === true;
    context.getContextUsage = () => read("getContextUsage") ?? undefined;
    context.getSystemPrompt = () => read("getSystemPrompt")?.prompt ?? "";
    context.getSystemPromptOptions = () => read("getSystemPromptOptions") ?? {};
    Object.defineProperty(runtime.contextValues, "model", {
      enumerable: true,
      get: () => {
        const info = read("getModelInfo");
        return info && info.id ? normalizeModel(info) : undefined;
      },
    });
    Object.defineProperty(runtime.state, "thinkingLevel", { enumerable: true, get: () => read("getThinkingLevel")?.level ?? "" });
    Object.defineProperty(runtime.state, "scopedModels", { enumerable: true, get: () => read("getScopedModels") ?? [] });
    context.sendMessage = (message, options) => runtime.call("sendMessage", { message, options: options ?? {} });
    context.sendUserMessage = (content, options) => runtime.call("sendUserMessage", { content, options: options ?? {} });
    return context;
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

  // pig additive (D19): Pi emits agent_settled in the microtasks after agent_end unless a handler on that tail waits on a timer, I/O or another real wait, during which Pi reads stdin (agent-session.ts:1044-1058,1752-1777; rpc-mode.ts:808-810). The host holds stdin through the tail, so a tail handler reports request_state suspended when its window closes unresponded, and the host then serves stdin until the handler answers.
  armSettleTailWindow(id, ctx) {
    const owner = this.requestContext.getStore();
    if (owner?.id === id && ctx.mode === "rpc") armRequestWindow(owner);
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
    const synchronousRPC = this.contextValues.mode === "rpc";
    const conn = this.conn;
    // A closed connection throws on write; the call itself then rejects as a host cancellation would.
    if (parentRequestId && !synchronousRPC && !conn.closed) conn.requestState(parentRequestId, "blocked", "host_call");
    let operation;
    operation = conn.call(method, args, parentRequestId).catch((err) => {
      // A host cancellation (the connection closed or the parent request was cancelled) and a call made after the close are not failures: Pi's calls are in-process and never fail at shutdown or with their caller, so they have nothing to report.
      if (isHostCancellation(err) || conn.closed) return;
      const reason = err && err.message ? err.message : String(err);
      try {
        process.stderr.write(`pig: host call ${method} failed: ${reason}\n`);
      } catch {
        // A closed stderr during shutdown must not escalate into an unhandled
        // rejection in a path the caller never awaits.
      }
    }).finally(() => {
      if (parentRequestId && !conn.closed) conn.requestState(parentRequestId, "progress");
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
