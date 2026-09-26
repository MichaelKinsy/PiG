import net from "node:net";
import { closeSync, openSync, readSync } from "node:fs";
import { basename } from "node:path";
import { pathToFileURL } from "node:url";
import { EventEmitter } from "node:events";
import { AsyncLocalStorage } from "node:async_hooks";
import { setRuntime } from "./state.mjs";
import { getKeybindings, isFocusable } from "./shims/pi-tui.mjs";
import { setCapabilities as setTerminalCapabilities } from "./shims/pi-dist/pi-tui/terminal-image.js";

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

class RuntimeSessionManager {
  constructor(runtime) { this.runtime = runtime; }
  getEntries() { this.runtime.ensureSessionLog(); return [...(this.runtime.state.session?.entries || [])]; }
  getBranch() { this.runtime.ensureSessionLog(); return this.runtime.branchFromEntries(); }
  getLeafId() { return this.runtime.state.session?.leafId || ""; }
  getSessionId() { return this.runtime.state.session?.sessionId || ""; }
  getSessionName() { return this.runtime.state.session?.sessionName || ""; }
  getSessionFile() { return this.runtime.state.session?.sessionFile || ""; }
}

// Session model discovery, request authentication, and model operations.
class RuntimeModelRegistry {
  constructor(runtime) { this.runtime = runtime; }
  find(provider, modelId) {
    return this.runtime.getModel(provider, modelId);
  }
  async getApiKeyAndHeaders(model) {
    return this.runtime.call("getModelAuth", { provider: providerIDFromModel(model), modelId: modelIDFromModel(model) });
  }
  stream(model, context, options = {}) {
    return this.runtime.startModelStream(model, context, options);
  }
  streamSimple(model, context, options = {}) {
    return this.runtime.startModelStream(model, context, options);
  }
  complete(model, context, options = {}) {
    return this.stream(model, context, options).result();
  }
}

class ThemeShim {
  constructor() {
    this.foregrounds = {};
    this.backgrounds = {};
  }
  setPalette(palette = {}) {
    this.foregrounds = palette.foregrounds && typeof palette.foregrounds === "object" ? palette.foregrounds : {};
    this.backgrounds = palette.backgrounds && typeof palette.backgrounds === "object" ? palette.backgrounds : {};
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
  bold(text) { return `\x1b[1m${String(text ?? "")}\x1b[22m`; }
  dim(text) { return `\x1b[2m${String(text ?? "")}\x1b[22m`; }
  italic(text) { return `\x1b[3m${String(text ?? "")}\x1b[23m`; }
  underline(text) { return `\x1b[4m${String(text ?? "")}\x1b[24m`; }
  inverse(text) { return `\x1b[7m${String(text ?? "")}\x1b[27m`; }
  strikethrough(text) { return `\x1b[9m${String(text ?? "")}\x1b[29m`; }
  getBashModeBorderColor() { return (text) => String(text ?? ""); }
}

class Connection {
  constructor(socket) {
    this.socket = socket;
    this.buffer = Buffer.alloc(0);
    this.pending = new Map();
    this.queue = [];
    this.waiters = [];
    this.closed = false;
    socket.on("data", (chunk) => this.onData(chunk));
    socket.on("close", () => this.onClose());
    socket.on("error", () => this.onClose());
  }

  onClose() {
    if (this.closed) return;
    this.closed = true;
    for (const waiter of this.waiters.splice(0)) waiter(null);
    for (const [, pending] of this.pending) {
      pending.reject(new Error("connection closed"));
    }
    this.pending.clear();
  }

  onData(chunk) {
    this.buffer = Buffer.concat([this.buffer, chunk]);
    while (this.buffer.length >= 4) {
      const size = this.buffer.readUInt32BE(0);
      if (this.buffer.length < 4 + size) return;
      const payload = this.buffer.subarray(4, 4 + size);
      this.buffer = this.buffer.subarray(4 + size);
      const env = JSON.parse(payload.toString("utf8"));
      if (env.type === "call_result") {
        const pending = this.pending.get(env.id);
        if (pending) {
          this.pending.delete(env.id);
          if (env.call_result?.error) {
            const info = env.call_result.error;
            const error = new Error(info.code ? `${info.code}: ${info.message}` : info.message);
            error.code = info.code || "";
            pending.reject(error);
          } else pending.resolve(env.call_result?.result ?? null);
        }
        continue;
      }
      if (this.waiters.length > 0) {
        this.waiters.shift()(env);
      } else {
        this.queue.push(env);
      }
    }
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
    this.socket.write(Buffer.concat([hdr, data]));
  }

  next() {
    if (this.queue.length > 0) return Promise.resolve(this.queue.shift());
    if (this.closed) return Promise.resolve(null);
    return new Promise((resolve) => this.waiters.push(resolve));
  }

  call(method, args = {}, parentRequestId = "") {
    const id = uuid();
    return new Promise((resolve, reject) => {
      this.pending.set(id, { resolve, reject, parentRequestId });
      this.send({ type: "call", id, call: { method, args, ...(parentRequestId ? { parent_request_id: parentRequestId } : {}) } });
    });
  }

  cancelParent(parentRequestId) {
    for (const [id, pending] of this.pending) {
      if (pending.parentRequestId !== parentRequestId) continue;
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
    this.ui = runtime.ui;
    this.hasUI = true;
    this.cwd = runtime.ready.cwd || process.cwd();
    // Run mode (tui|rpc|json|print). Extensions gate on it: pi-atelier returns
    // early from session_start unless it is "tui": so leaving it undefined
    // silently disables them rather than failing.
    this.mode = runtime.ready.mode || "tui";
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
  shutdown() { this.runtime.fireAndForget("shutdown", {}); }
  getContextUsage() { return this.runtime.state.contextUsage; }
  compact(options = {}) { this.runtime.fireAndForget("compact", { options }); }
  getSystemPrompt() { return this.runtime.state.systemPrompt || this.runtime.systemPrompt || ""; }
  getSystemPromptOptions() { return this.runtime.state.systemPromptOptions ?? {}; }
  async waitForIdle() { return this.runtime.call("waitForIdle", {}); }
  async newSession(options = {}) { return this.runtime.call("newSession", { options }); }
  async fork(entryId, options = {}) { return this.runtime.call("fork", { entryId, options }); }
  async navigateTree(targetId, options = {}) { return this.runtime.call("navigateTree", { ...options, targetId }); }
  async switchSession(sessionPath, options = {}) { return this.runtime.call("switchSession", { sessionPath, options }); }
  async reload() { return this.runtime.call("reload", {}); }
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
    if (Array.isArray(content)) {
      this.runtime.widgets.delete(key);
      const lines = content.map((v) => String(v));
      if (options?.placement) {
        this.runtime.fireAndForget("ui.setWidget", { key, content: lines, options });
      } else {
        this.runtime.conn?.pushWidget(key, lines);
      }
      return;
    }
    if (typeof content === "function") {
      this.runtime.setWidgetFactory(key, content, options);
      return;
    }
    throw new Error("setWidget content must be a component factory, string array, or undefined");
  }
  setFooter(factory) {
    this.runtime.footerFactory = typeof factory === "function" ? factory : undefined;
    this.runtime.specialSurfaceComponents.delete("footer");
    this.runtime.renderSpecialSurface("footer");
  }
  setHeader(factory) {
    this.runtime.headerFactory = typeof factory === "function" ? factory : undefined;
    this.runtime.specialSurfaceComponents.delete("header");
    this.runtime.renderSpecialSurface("header");
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
  addAutocompleteProvider(factory) {
    if (typeof factory !== "function") return;
    // Subprocess shim: we keep an ordered list of factories. Each
    // request from the host runs them as a fold (matching upstream
    // chain semantics) starting from a null-base provider that
    // returns no suggestions. Filter-style wrappers that conditionally
    // delegate to `current.getSuggestions` therefore see a base that
    // returns null: they act as additive providers, not filters of
    // the pig built-ins. This is the only honest mapping when the
    // built-ins live on a different process.
    if (!this.runtime.autocompleteFactories) {
      this.runtime.autocompleteFactories = [];
    }
    const id = `ac${this.runtime.autocompleteFactories.length + 1}`;
    this.runtime.autocompleteFactories.push(factory);
    if (!this.runtime.autocompleteRegistered) {
      this.runtime.autocompleteRegistered = true;
      this.runtime.fireAndForget("ui.addAutocompleteProvider", { providerId: id });
    }
  }
  setEditorComponent(factory) {
    if (typeof factory !== "function") {
      this.runtime.fireAndForget("ui.setEditorComponent", { clear: true });
      this.runtime.editorComponent = undefined;
      return;
    }
    // Subprocess shim: function factories can't cross a process
    // boundary, but the prompt-editor pattern (modeLabel + history +
    // bash-mode border color) reduces to a small bounded decoration
    // set that we can serialise. We invoke the factory locally with a
    // shim CustomEditor that captures the decoration state, then
    // forward it to the host. The editor stays in Go; the Node side
    // never touches per-keystroke input.
    this.runtime.applyEditorComponent(factory);
  }
  getAllThemes() { return this.runtime.state.allThemes ?? []; }
  getTheme(name) {
    // Upstream getTheme loads a theme by name without switching to it. The
    // host pushes the theme list, so an unknown name reports absence rather
    // than silently yielding the active theme.
    if (name === undefined || name === null) return this.theme;
    return (this.runtime.state.allThemes ?? []).find((t) => t.name === name);
  }
  setTheme(name) { this.runtime.fireAndForget("ui.setTheme", { name }); return { success: true }; }
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

function oauthCredsToWire(creds) {
  creds = creds || {};
  const wire = { refresh: creds.refresh ?? "", access: creds.access ?? "", expires: creds.expires ?? 0 };
  if (creds.projectId) wire.projectId = creds.projectId;
  return wire;
}

function oauthCredsFromWire(wire) {
  wire = wire || {};
  const creds = { refresh: wire.refresh ?? "", access: wire.access ?? "", expires: wire.expires ?? 0 };
  if (wire.projectId) creds.projectId = wire.projectId;
  return creds;
}

// OAuthLoginCallbacks presents the upstream config.oauth callback surface to a
// provider's login() closure and forwards each call to the host over oauth.cb.*.
// Value-returning callbacks resolve the host reply; a host cancel becomes an
// undefined return (onSelect) or a thrown cancel (onPrompt/onManualCodeInput),
// matching the upstream in-process callback semantics.
class OAuthLoginCallbacks {
  constructor(runtime) {
    this.runtime = runtime;
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

export class Runtime {
  constructor(entry) {
    this.entry = entry;
    this.name = process.env.PIG_EXT_NAME || basename(entry).replace(/\.[^.]+$/, "");
    this.version = "0.0.0";
    this.localEvents = new EventEmitter();
    this.requestContext = new AsyncLocalStorage();
    this.activeRequests = new Map();
    this.requestTasks = new Set();
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
    this.providers = new Map();
    this.oauthProviders = new Map();
    this.renderers = new Map();
    this.entryRenderers = new Map();
    // Upstream ToolExecutionComponent keeps one renderer state per tool card,
    // shared by renderCall and renderResult, and each renderer's last
    // component. The host names the card and releases it when the card is gone.
    this.toolRenderCards = new Map();
    this.modelStreams = new Map();
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
      contextUsage: { tokens: 0, contextWindow: 0, percent: 0 },
      systemPrompt: "",
      systemPromptOptions: {},
      flags: {},
      hasUI: true,
      footerData: { gitBranch: "", extensionStatuses: {}, availableProviderCount: 0 },
    };
    this.contextUsage = this.state.contextUsage;
    this.systemPrompt = "";
    this.ui = new RuntimeUI(this);
    this.ctx = new RuntimeContext(this);
    this.api = this.buildAPI();
    setRuntime(this);
  }

  buildAPI() {
    // Upstream createExtensionRuntime: action methods throw while the factory
    // runs, before the runtime is bound to the host.
    const action = (fn) => (...args) => {
      if (!this.conn) throw new Error("Extension runtime not initialized. Action methods cannot be called during extension loading.");
      return fn(...args);
    };
    return {
      on: (event, handler) => this.on(event, handler),
      registerTool: (definition) => this.registerTool(definition),
      registerCommand: (name, definition) => this.registerCommand(name, definition),
      registerShortcut: (key, definition) => this.registerShortcut(key, definition),
      registerFlag: (name, definition) => this.registerFlag(name, definition),
      registerMessageRenderer: (customType, handler) => this.registerMessageRenderer(customType, handler),
      registerEntryRenderer: (customType, handler) => this.registerEntryRenderer(customType, handler),
      registerProvider: (name, config) => this.registerProvider(name, config),
      unregisterProvider: (name) => this.providers.delete(name),
      sendMessage: action((message, options = {}) => this.fireAndForget("sendMessage", { message, options })),
      sendUserMessage: action((content, options = {}) => this.fireAndForget("sendUserMessage", { content, options })),
      appendEntry: action((customType, data) => this.fireAndForget("appendEntry", { customType, data })),
      messageRole: (data) => Runtime.messageRole(data),
      messageText: (data) => Runtime.messageText(data),
      getSessionName: action(() => this.state.session?.sessionName || undefined),
      setSessionName: action((name) => this.fireAndForget("setSessionName", { name })),
      setLabel: action((entryId, label) => this.fireAndForget("setLabel", { entryId, label })),
      exec: (command, args = [], options = undefined) => this.call("exec", { command, args, options }),
      getFlag: (name) => this.state.flags?.[name] ?? this.flagValues?.[name] ?? undefined,
      getActiveTools: action(() => [...(this.state.activeTools || [])]),
      // Pi returns fresh ToolInfo and SlashCommandInfo objects on every call;
      // a caller mutating one must not change the replicated host state.
      getAllTools: action(() => structuredClone(this.state.allTools || [])),
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
      setModel: (model) => {
        if (!this.conn) return Promise.reject(new Error("Extension runtime not initialized"));
        this.ready.model = model;
        this.state.model = normalizeModel({ id: model });
        this.ctx.model = this.state.model;
        this.fireAndForget("setModel", { model });
      },
      events: {
        on: (event, handler) => this.localEvents.on(event, handler),
        emit: (event, data) => this.localEvents.emit(event, data),
      },
    };
  }

  on(event, handler) {
    const bucket = this.handlers.get(event) ?? [];
    bucket.push({ id: this.nextHandlerId++, handler });
    this.handlers.set(event, bucket);
  }

  registerTool(definition) {
    if (!definition || !definition.name) throw new Error("registerTool requires a definition with name");
    this.tools.set(definition.name, definition);
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
    this.flags.set(name, { name, ...definition });
  }

  registerMessageRenderer(customType, handler) {
    this.renderers.set(customType, handler);
  }

  registerEntryRenderer(customType, handler) {
    this.entryRenderers.set(customType, handler);
  }

  async connect() {
    const sockPath = process.env.PIG_EXT_SOCKET;
    if (!sockPath) throw new Error("PIG_EXT_SOCKET not set");
    const socket = await new Promise((resolve, reject) => {
      const s = net.createConnection(sockPath, () => resolve(s));
      s.on("error", reject);
    });
    this.conn = new Connection(socket);
    this.removeDrainListener = this.conn.onDrain(() => {
      for (const [key, widget] of this.widgets) {
        if (widget.renderRequested) this.requestWidgetRender(key);
      }
      for (const overlay of this.customOverlays.values()) {
        if (overlay.renderRequested) overlay.renderFrame();
      }
    });
    this.conn.send({
      type: "register",
      register: {
        name: this.name,
        version: this.version,
        tools: [...this.tools.values()].map((tool) => ({
          name: tool.name,
          label: tool.label,
          description: tool.description || "",
          parameters: tool.parameters || { type: "object", properties: {} },
          constrained_sampling: tool.constrainedSampling,
          execution_mode: tool.executionMode,
          prompt_guidelines: tool.promptGuidelines,
          annotations: tool.annotations,
          source: tool.source,
          render_shell: tool.renderShell === "self" ? "self" : undefined,
          renders_call: typeof tool.renderCall === "function" || undefined,
          renders_result: typeof tool.renderResult === "function" || undefined,
        })),
        commands: [...this.commands.values()].map((cmd) => ({
          name: cmd.name,
          description: cmd.description || "",
          args: cmd.args,
        })),
        shortcuts: [...this.shortcuts.values()].map((s) => ({ key: s.key, description: s.description || "" })),
        handlers: [...this.handlers.entries()].flatMap(([event, handlers]) =>
          handlers.map(({ id }) => ({ event, can_block: BLOCKING_EVENTS.has(event), handler_id: id })),
        ),
        flags: [...this.flags.values()].map((f) => ({ name: f.name, description: f.description || "", type: f.type || "string", default: f.default })),
        providers: [...this.providers.entries()].map(([name, config]) => ({ name, config })),
        message_renderers: [...this.renderers.keys()].map((customType) => ({ custom_type: customType })),
        entry_renderers: [...this.entryRenderers.keys()].map((customType) => ({ custom_type: customType })),
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
  }

  async run() {
    await this.connect();
    try {
      while (true) {
        const env = await this.conn.next();
        if (!env) return;
        if (env.type === "ready") {
          this.ready = env.ready || this.ready;
          this.replaceModels(this.ready.models);
          this.ctx.cwd = this.ready.cwd || this.ctx.cwd;
          this.ctx.mode = this.ready.mode || this.ctx.mode;
          this.ctx.model = this.ready.model ? { id: this.ready.model } : this.ctx.model;
          if (env.ready?.state) this.applyState(env.ready.state);
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
          this.activeRequests.get(requestId)?.abort(env.cancel?.reason || "cancelled");
          this.conn.cancelParent(requestId);
          continue;
        }
        if (env.type === "request") {
          this.conn.requestState(env.id, "started");
          const controller = new AbortController();
          this.activeRequests.set(env.id, controller);
          const requestCtx = Object.create(this.ctx);
          requestCtx.signal = controller.signal;
          // Dispatch concurrently so long-running interactive calls
          // (e.g. ui.custom blocking on done()) don't starve incoming
          // notifications such as ui.custom.input. The handler will
          // serialize its response back through the connection on its
          // own when it resolves.
          const task = this.requestContext.run({ id: env.id, controller, pendingHostCalls: new Set() }, async () => {
            try {
              await this.handleRequest(env.id, env.request || {}, requestCtx);
            } finally {
              this.activeRequests.delete(env.id);
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
    this.conn?.pushWidget(key, lines, width);
  }

  applyState(snapshot) {
    if (!snapshot || typeof snapshot !== "object") return;
    for (const key of ["activeTools", "allTools", "commands"]) {
      if (Array.isArray(snapshot[key])) this.state[key] = [...snapshot[key]];
    }
    if (typeof snapshot.thinkingLevel === "string") this.state.thinkingLevel = snapshot.thinkingLevel;
    if (snapshot.model && typeof snapshot.model === "object") this.state.model = normalizeModel(snapshot.model);
    if (snapshot.session && typeof snapshot.session === "object") {
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
        entries: this.applyEntryAppend(snapshot.session),
      };
      this.branchCache = undefined;
    }
    if (typeof snapshot.isIdle === "boolean") this.state.isIdle = snapshot.isIdle;
    if (typeof snapshot.projectTrusted === "boolean") this.state.projectTrusted = snapshot.projectTrusted;
    if (snapshot.footerData && typeof snapshot.footerData === "object") {
      const previousBranch = this.state.footerData.gitBranch;
      this.state.footerData = {
        gitBranch: snapshot.footerData.gitBranch ?? "",
        extensionStatuses: snapshot.footerData.extensionStatuses ?? {},
        availableProviderCount: snapshot.footerData.availableProviderCount ?? 0,
      };
      if (this.state.footerData.gitBranch !== previousBranch) this.notifyBranchChange();
    }
    if (typeof snapshot.hasPendingMessages === "boolean") this.state.hasPendingMessages = snapshot.hasPendingMessages;
    if (typeof snapshot.editorText === "string") this.state.editorText = snapshot.editorText;
    if (typeof snapshot.toolsExpanded === "boolean") this.state.toolsExpanded = snapshot.toolsExpanded;
    if (Array.isArray(snapshot.allThemes)) this.state.allThemes = [...snapshot.allThemes];
    if (snapshot.contextUsage && typeof snapshot.contextUsage === "object") {
      this.state.contextUsage = { ...this.state.contextUsage, ...snapshot.contextUsage };
      this.contextUsage = this.state.contextUsage;
    }
    if (snapshot.systemPromptOptions && typeof snapshot.systemPromptOptions === "object") {
      this.state.systemPromptOptions = snapshot.systemPromptOptions;
    }
    if (typeof snapshot.systemPrompt === "string") {
      this.state.systemPrompt = snapshot.systemPrompt;
      this.systemPrompt = snapshot.systemPrompt;
    }
    if (snapshot.flags && typeof snapshot.flags === "object") this.state.flags = { ...snapshot.flags };
    // Pi's TUI and its extensions share one capability cache; here the host
    // owns the terminal, so its resolved capabilities seed the cache pi-tui's
    // Markdown reads.
    const caps = snapshot.terminalCapabilities;
    if (caps && typeof caps === "object") {
      setTerminalCapabilities({ images: caps.images || null, trueColor: caps.trueColor === true, hyperlinks: caps.hyperlinks === true });
    }
    if (typeof snapshot.hasUI === "boolean") this.state.hasUI = snapshot.hasUI;
    this.ctx.hasUI = this.state.hasUI;
    this.ctx.model = normalizeModel(this.state.model);
    this.renderSpecialSurface("header");
    this.renderSpecialSurface("footer");
  }

  // applyEditorComponent invokes a setEditorComponent factory locally,
  // captures the resulting decoration state (border color, mode label,
  // history) and forwards it to the host. The actual editor remains
  // pig-native: only decoration data crosses the socket.
  //
  // pig-specific: real CustomEditor instances would route every
  // keystroke through the socket which is unacceptable latency for an
  // interactive editor. The decoration-only path covers the dominant
  // use case in real extensions (prompt-editor).
  applyEditorComponent(factory) {
    let comp;
    try {
      comp = factory(this.api?.tui || {}, this.ui?.theme, getKeybindings());
    } catch (err) {
      this.fireAndForget("ui.notify", {
        message: `setEditorComponent factory failed: ${err?.message || String(err)}`,
        level: "error",
      });
      return;
    }
    if (!comp) {
      this.fireAndForget("ui.setEditorComponent", { clear: true });
      this.editorComponent = undefined;
      return;
    }
    this.editorComponent = comp;
    const send = () => {
      const deco = this.snapshotEditorDecoration(comp);
      const history = Array.isArray(comp._history) ? comp._history.slice() : [];
      this.fireAndForget("ui.setEditorComponent", {
        clear: false,
        decoration: deco,
        history,
      });
    };
    // Hook requestRenderNow so dynamic mode-label changes propagate.
    if (typeof comp._onRequestRender === "undefined") {
      Object.defineProperty(comp, "_onRequestRender", {
        value: send,
        writable: true,
        configurable: true,
      });
    } else {
      comp._onRequestRender = send;
    }
    send();
  }

  // snapshotEditorDecoration produces a serialisable decoration shape
  // for the pig editor. It probes the captured borderColor and
  // modeLabelColor callbacks with a sentinel to extract their
  // ANSI-wrapping prefix/suffix without serialising the closure itself.
  snapshotEditorDecoration(comp) {
    const sentinel = "\u0000pig-deco\u0000";
    const splitANSI = (styled) => {
      const idx = styled.indexOf(sentinel);
      if (idx < 0) return { prefix: "", suffix: "" };
      return { prefix: styled.slice(0, idx), suffix: styled.slice(idx + sentinel.length) };
    };
    let labelText = "";
    if (typeof comp._modeLabelProvider === "function") {
      try { labelText = String(comp._modeLabelProvider() ?? ""); } catch { labelText = ""; }
    }
    let labelPrefix = "", labelSuffix = "";
    if (typeof comp._modeLabelColor === "function") {
      try {
        const probed = String(comp._modeLabelColor(sentinel));
        ({ prefix: labelPrefix, suffix: labelSuffix } = splitANSI(probed));
      } catch {}
    }
    let borderPrefix = "", borderSuffix = "";
    if (typeof comp._borderColor === "function") {
      try {
        const probed = String(comp._borderColor(sentinel));
        ({ prefix: borderPrefix, suffix: borderSuffix } = splitANSI(probed));
      } catch {}
    }
    return {
      label: labelText,
      labelPrefix,
      labelSuffix,
      borderPrefix,
      borderSuffix,
      lockBorder: Boolean(comp._lockedBorder),
    };
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
    const owner = this.requestContext.getStore();
    let operation;
    operation = this.syncSessionLog(entries.length).catch((error) => {
      try { process.stderr.write(`pig: session synchronization failed: ${error?.message || String(error)}\n`); } catch {}
    }).finally(() => {
      owner?.pendingHostCalls.delete(operation);
    });
    owner?.pendingHostCalls.add(operation);
    this.sessionLogSync = operation;
  }

  async syncSessionLog(startCursor) {
    let cursor = startCursor;
    let complete = false;
    for (;;) {
      const result = await this.call("watchSessionLog", { cursor, complete });
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
    if (!session.leafId) {
      this.branchCache = [...entries];
      this.branchCacheLeaf = session.leafId;
      return [...this.branchCache];
    }
    const byId = new Map();
    for (const entry of entries) {
      if (entry && entry.id !== undefined) byId.set(entry.id, entry);
    }
    const path = [];
    const seen = new Set();
    let current = byId.get(session.leafId);
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
      getGitBranch() { return self.state.footerData.gitBranch || undefined; },
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
    if (!factory) {
      this.fireAndForget(kind === "footer" ? "ui.setFooter" : "ui.setHeader", { clear: true });
      return;
    }
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
    if (isFocusable(component) && !(isOverlay && overlayOpts?.nonCapturing)) component.focused = true;
    const openArgs = {
      key,
      title: String(overlayOpts?.title || ""),
      widthFraction: Number(overlayOpts?.widthFraction || 0) || 0,
      heightFraction: Number(overlayOpts?.heightFraction || 0) || 0,
      overlay: isOverlay,
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
    switch (notify.method) {
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
      case "model_registry_update": {
        let args = notify.args;
        if (typeof args === "string") args = JSON.parse(args);
        this.replaceModels(args?.models);
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
          // Re-render widgets: height-dependent layouts (chain graphs,
          // dashboards) may want to reflow.
          for (const key of this.widgets.keys()) this.renderWidget(key);
          for (const overlay of this.customOverlays?.values() || []) {
            overlay.renderFrame();
          }
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
    config = config || {};
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
  }

  unregisterProvider(name) {
    this.providers.delete(name);
    this.oauthProviders.delete(name);
  }

  // dispatchOAuth answers an oauth_* request. The provider is keyed by
  // request.tool because oauth_* frames carry no provider field of their own.
  async dispatchOAuth(id, request) {
    const provider = this.oauthProviders.get(request.tool);
    if (!provider) throw new Error(`unknown oauth provider: ${request.tool}`);
    switch (request.method) {
      case "oauth_login": {
        if (typeof provider.login !== "function") throw new Error("provider does not support login");
        const creds = await provider.login(new OAuthLoginCallbacks(this));
        await this.respond(id, oauthCredsToWire(creds));
        return;
      }
      case "oauth_refresh": {
        if (typeof provider.refreshToken !== "function") throw new Error("provider does not support refresh");
        const creds = await provider.refreshToken(oauthCredsFromWire(request.args));
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
        case "command": {
          const cmd = this.commands.get(request.tool);
          if (!cmd) throw new Error(`unknown command: ${request.tool}`);
          await cmd.handler?.(request.args ?? "", ctx);
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
          if (fileOps) {
            for (const key of ["read", "written", "edited"]) fileOps[key] = new Set(fileOps[key] ?? []);
          }
          if (request.event === "agent_before_settle") {
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
          const messages = event.messages;
          const snapshot = Array.isArray(messages) ? messages.slice() : undefined;
          const pending = selected.handler(event, ctx);
          // Calling an async handler executes its synchronous prefix before
          // returning its Promise. Admit the next prompt at that boundary.
          if (request.event === "ui_prompt_start" || request.event === "ui_prompt_end") {
            this.conn.requestState(id, "blocked", "external_io");
          }
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
          // Build the chained provider on demand. Base provider is
          // null-returning so chained wrappers fall back gracefully.
          const factories = this.autocompleteFactories || [];
          const baseProvider = {
            async getSuggestions() { return null; },
            applyCompletion(lines, cursorLine, cursorCol, _item, _prefix) { return [lines, cursorLine, cursorCol]; },
            shouldTriggerFileCompletion() { return false; },
          };
          const chain = factories.reduce((acc, f) => {
            try { return f(acc) || acc; } catch { return acc; }
          }, baseProvider);
          const args = request.args || {};
          const lines = Array.isArray(args.lines) ? args.lines : [];
          const cursorLine = Number(args.cursorLine) || 0;
          const cursorCol = Number(args.cursorCol) || 0;
          const ac = new AbortController();
          const options = { signal: ac.signal };
          const abort = () => ac.abort(ctx.signal?.reason);
          ctx.signal?.addEventListener("abort", abort, { once: true });
          let result = null;
          try {
            result = await chain.getSuggestions(lines, cursorLine, cursorCol, options);
          } finally {
            ctx.signal?.removeEventListener("abort", abort);
          }
          const items = (result && Array.isArray(result.items)) ? result.items : [];
          const prefix = (result && typeof result.prefix === "string") ? result.prefix : "";
          await this.respond(id, { items, prefix });
          return;
        }
        case "terminal_input": {
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
          await this.dispatchOAuth(id, request);
          return;
        default:
          throw new Error(`unknown request method: ${request.method}`);
      }
    } catch (err) {
      await this.respond(id, null, err instanceof Error ? err : new Error(String(err)));
    }
  }

  async respond(id, result = null, error = null) {
    const owner = this.requestContext.getStore();
    if (owner?.id === id && owner.pendingHostCalls.size > 0) {
      await Promise.all([...owner.pendingHostCalls]);
    }
    this.conn.respond(id, result, error);
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
    };
  }

  startModelStream(model, context, options = {}) {
    const streamId = `model-stream-${this.nextModelStreamId++}`;
    const stream = new ModelEventStream();
    this.modelStreams.set(streamId, stream);
    // An AbortSignal (upstream options.signal) cannot cross the process
    // boundary: its abort cancels the host request instead, and the provider
    // ends the stream as upstream does for an aborted request.
    const { signal, ...requestOptions } = options ?? {};
    let onAbort;
    if (signal && typeof signal.addEventListener === "function") {
      onAbort = () => { this.call("cancelModelStream", { streamId }).catch(() => {}); };
      signal.addEventListener("abort", onAbort, { once: true });
    }
    // The host writes every model_stream_event notification before the call
    // result, but Connection resolves call results inside its frame reader
    // while earlier notifications still wait for the run loop. Settle only
    // after the run loop has applied every frame received before the result.
    const settle = (error) => {
      if (!stream.terminal && this.conn?.queue.length > 0) {
        setImmediate(() => settle(error));
        return;
      }
      if (!stream.terminal) stream.fail(error ?? new Error("model stream ended without a terminal event"), model);
      this.modelStreams.delete(streamId);
      if (onAbort) signal.removeEventListener("abort", onAbort);
    };
    void this.call("modelStream", { streamId, model, request: { ...context, ...requestOptions } }).then(
      () => setImmediate(() => settle()),
      (error) => setImmediate(() => settle(error)),
    );
    if (signal?.aborted) onAbort?.();
    return stream;
  }

  async call(method, args = {}) {
    if (!this.conn) throw new Error("runtime not connected");
    const parentRequestId = this.requestContext.getStore()?.id || "";
    if (parentRequestId && !USER_BLOCKING_CALLS.has(method)) {
      this.conn.requestState(parentRequestId, "blocked", "host_call");
    }
    try {
      return await this.conn.call(method, args, parentRequestId);
    } finally {
      if (parentRequestId) this.conn.requestState(parentRequestId, "progress");
    }
  }

  blockForUser() {
    const requestId = this.requestContext.getStore()?.id || "";
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

  fireAndForget(method, args = {}) {
    if (!this.conn) return;
    // The caller does not await, but the dispatcher owns the operation through
    // its parent request. The response cannot report completed while this call
    // is still blocked, and a host rejection remains visible on the existing
    // stderr path.
    const owner = this.requestContext.getStore();
    const parentRequestId = owner?.id || "";
    if (parentRequestId) this.conn.requestState(parentRequestId, "blocked", "host_call");
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

export async function loadExtension(entry) {
  const runtime = new Runtime(entry);
  const mod = await import(pathToFileURL(entry).href);
  const install = mod?.default ?? mod;
  if (typeof install !== "function") {
    throw new Error(`Extension does not export a valid factory function: ${entry}`);
  }
  await install(runtime.api);
  await runtime.run();
}
