import {
  FileAuthStorageBackend,
  isClientMetadataDocumentCallback,
  resolveConfigValueOrThrow,
  resolveHeadersOrThrow
} from "./chunk-H3JRSATQ.js";
import {
  sliceTail
} from "./chunk-BXLOEZ7M.js";
import {
  APP_NAME,
  VERSION,
  getAgentDir
} from "./chunk-JGRZB36T.js";
import {
  __name
} from "./chunk-FIOAHQVQ.js";

// pi-dist/pi-coding-agent/extensions/mcp/runtime.js
import { homedir } from "node:os";
import { basename, join as join2, resolve } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import { pathToFileURL } from "node:url";
import { JSON_RPC_ERROR_CODES, McpAuthRequiredError, McpClient, McpError, McpHttpError, McpSessionExpiredError, StdioTransport, StreamableHttpTransport } from "../../pi-mcp/index.js";
import { McpOAuthAuthorizationRequiredError } from "../../pi-mcp/oauth/index.js";

// pi-dist/pi-coding-agent/extensions/mcp/oauth.js
import { createHash } from "node:crypto";
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { oauthErrorHtml, oauthSuccessHtml } from "../../pi-ai/utils/oauth-page.js";
import { mcpNamespace } from "../../pi-mcp/index.js";
import { completeMcpSignIn, createMcpAuthProvider as createAuthProvider, listenForLoopbackSignIn, McpRefreshLockBusyError, startMcpSignIn, waitForLoopbackSignIn } from "../../pi-mcp/oauth/index.js";
import lockfile from "../../../proper-lockfile.mjs";
var REFRESH_LOCK_STALE_MS = 2e4;
var REFRESH_LOCK_WAIT_MS = 25e3;
var REFRESH_LOCK_RETRY_MS = 100;
function parseStates(content) {
  if (!content?.trim())
    return {};
  const parsed = JSON.parse(content);
  return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed) ? parsed : {};
}
__name(parseStates, "parseStates");
function serializeStates(states) {
  return `${JSON.stringify(states, null, 2)}
`;
}
__name(serializeStates, "serializeStates");
function storeKeys(name, serverUrl) {
  const legacyKey = String(new URL(serverUrl));
  return { key: `${mcpNamespace(name)}|${legacyKey}`, legacyKey };
}
__name(storeKeys, "storeKeys");
var McpOAuthCredentialStore = class {
  static {
    __name(this, "McpOAuthCredentialStore");
  }
  backend;
  /** Directory for the refresh lock files. Without one, refreshes are only serialized in this process. */
  lockDir;
  constructor(backend, lockDir) {
    this.backend = backend ?? new FileAuthStorageBackend(join(getAgentDir(), "mcp-auth.json"));
    this.lockDir = backend ? lockDir : getAgentDir();
  }
  forServer(name, serverUrl) {
    const { key, legacyKey } = storeKeys(name, serverUrl);
    return {
      // The first server to load legacy state takes it over; others with the same URL sign in again.
      load: /* @__PURE__ */ __name(() => this.backend.withLock((current) => {
        const states = parseStates(current);
        if (states[key] || !states[legacyKey])
          return { result: states[key] };
        states[key] = states[legacyKey];
        delete states[legacyKey];
        return { result: states[key], next: serializeStates(states) };
      }), "load"),
      save: /* @__PURE__ */ __name((state) => this.write((states) => {
        states[key] = state;
      }), "save"),
      withRefreshLock: /* @__PURE__ */ __name((fn) => this.withRefreshLock(key, fn), "withRefreshLock")
    };
  }
  /**
   * A lock file per server. When the process exits, proper-lockfile removes the locks it holds; when
   * it is killed, the lock goes stale because it is no longer renewed, and the next process takes it over.
   * Throws `McpRefreshLockBusyError` when another process holds the lock for the whole wait.
   */
  async withRefreshLock(key, fn) {
    if (!this.lockDir)
      return fn();
    mkdirSync(this.lockDir, { recursive: true, mode: 448 });
    const hash = createHash("sha256").update(key).digest("hex").slice(0, 16);
    const release = await lockfile.lock(join(this.lockDir, `mcp-auth-refresh-${hash}`), {
      realpath: false,
      stale: REFRESH_LOCK_STALE_MS,
      retries: {
        retries: REFRESH_LOCK_WAIT_MS / REFRESH_LOCK_RETRY_MS,
        factor: 1,
        minTimeout: REFRESH_LOCK_RETRY_MS,
        maxTimeout: REFRESH_LOCK_RETRY_MS
      },
      // The default throws from a timer. A lost lock at worst lets two refreshes overlap.
      onCompromised: /* @__PURE__ */ __name(() => {
      }, "onCompromised")
    }).catch((error) => {
      if (error instanceof Error && "code" in error && error.code === "ELOCKED")
        throw new McpRefreshLockBusyError();
      throw error;
    });
    try {
      return await fn();
    } finally {
      await release().catch(() => void 0);
    }
  }
  /** The stored tokens of a server, for noticing sign-ins done by another process. Does not take over legacy state. */
  tokens(name, serverUrl) {
    const { key, legacyKey } = storeKeys(name, serverUrl);
    const states = this.backend.withLock((current) => ({ result: parseStates(current) }));
    return (states[key] ?? states[legacyKey])?.tokens;
  }
  /** Returns whether credentials were stored for the server. Removes legacy state the server would take over. */
  remove(name, serverUrl) {
    const { key, legacyKey } = storeKeys(name, serverUrl);
    return this.backend.withLock((current) => {
      const states = parseStates(current);
      const stored = key in states ? key : legacyKey in states ? legacyKey : void 0;
      if (!stored)
        return { result: false };
      delete states[stored];
      return { result: true, next: serializeStates(states) };
    });
  }
  write(update) {
    this.backend.withLock((current) => {
      const states = parseStates(current);
      update(states);
      return { result: void 0, next: serializeStates(states) };
    });
  }
};
var PI_CLIENT_METADATA_DOCUMENTS = {
  baseUrl: "${identity.hostedOrigin}/oauth",
  listsRedirectUrl: isClientMetadataDocumentCallback
};
var PI_CLIENT = { clientName: APP_NAME, clientMetadataDocuments: PI_CLIENT_METADATA_DOCUMENTS };
function piSettings(settings) {
  const auto = (settings.clientRegistration ?? "auto") === "auto";
  return auto && APP_NAME !== "pi" ? { ...settings, clientRegistration: "dcr" } : settings;
}
__name(piSettings, "piSettings");
function createMcpAuthProvider(options) {
  return createAuthProvider({ ...options, ...PI_CLIENT, settings: /* @__PURE__ */ __name(() => piSettings(options.settings()), "settings") });
}
__name(createMcpAuthProvider, "createMcpAuthProvider");
function renderCallbackPage(page) {
  return page.ok ? oauthSuccessHtml("Signed in to the MCP server. You may now close this page.") : oauthErrorHtml(page.message, page.details);
}
__name(renderCallbackPage, "renderCallbackPage");
async function signInMcpServer(options) {
  const { serverUrl, store, challenge, prompt, signal } = options;
  const settings = piSettings(options.settings);
  const { callback, redirectUrl } = await listenForLoopbackSignIn({
    serverUrl,
    store,
    settings,
    clientMetadataDocuments: PI_CLIENT_METADATA_DOCUMENTS,
    renderPage: renderCallbackPage
  });
  try {
    const signIn = { serverUrl, store, settings, redirectUrl, challenge, signal, ...PI_CLIENT };
    const authorization = await startMcpSignIn(signIn);
    if (authorization.kind === "authorized")
      return;
    prompt.showAuthorizationUrl(new URL(authorization.authorizationUrl));
    const response = await waitForLoopbackSignIn({
      callback,
      authorization,
      promptForRedirectUrl: /* @__PURE__ */ __name((promptSignal) => prompt.promptForRedirectUrl(promptSignal), "promptForRedirectUrl"),
      signal
    });
    await completeMcpSignIn({ ...signIn, authorization, response });
  } finally {
    await callback.close();
  }
}
__name(signInMcpServer, "signInMcpServer");

// pi-dist/pi-coding-agent/extensions/mcp/runtime.js
import { McpSignInCancelledError } from "../../pi-mcp/oauth/index.js";

// pi-dist/pi-coding-agent/extensions/mcp/log.js
import { appendFileSync, mkdirSync as mkdirSync2, renameSync, statSync } from "node:fs";
import { dirname } from "node:path";
var MAX_LOG_BYTES = 5 * 1024 * 1024;
function isRecord(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
__name(isRecord, "isRecord");
function formatData(data) {
  if (typeof data === "string")
    return data;
  try {
    return JSON.stringify(data) ?? String(data);
  } catch {
    return String(data);
  }
}
__name(formatData, "formatData");
function formatMcpLogMessage(server, params, now = /* @__PURE__ */ new Date()) {
  const message = isRecord(params) ? params : { data: params };
  const level = typeof message.level === "string" ? message.level : "info";
  const logger = typeof message.logger === "string" && message.logger ? ` ${message.logger}:` : "";
  const text = formatData(message.data).replace(/\r?\n/g, "\n    ");
  return `${now.toISOString()} [${server}] ${level}${logger} ${text}
`;
}
__name(formatMcpLogMessage, "formatMcpLogMessage");
var McpServerLog = class {
  static {
    __name(this, "McpServerLog");
  }
  path;
  size;
  constructor(path) {
    this.path = path;
  }
  write(server, params) {
    const line = formatMcpLogMessage(server, params);
    try {
      if (this.size === void 0) {
        mkdirSync2(dirname(this.path), { recursive: true });
        this.size = this.currentSize();
      }
      if (this.size > MAX_LOG_BYTES) {
        if (this.currentSize() > MAX_LOG_BYTES)
          renameSync(this.path, `${this.path}.1`);
        this.size = this.currentSize();
      }
      appendFileSync(this.path, line);
      this.size += Buffer.byteLength(line);
    } catch {
    }
  }
  currentSize() {
    try {
      return statSync(this.path).size;
    } catch {
      return 0;
    }
  }
};

// pi-dist/pi-coding-agent/extensions/mcp/runtime.js
var DEFAULT_TIMEOUT_SECONDS = 60;
var STDERR_TAIL_CHARS = 2e3;
var CONNECT_RETRY_DELAYS_MS = [250, 1e3];
function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}
__name(errorMessage, "errorMessage");
function isTransientError(error) {
  if (error instanceof McpHttpError) {
    return error.status === 408 || error.status === 429 || error.status >= 500 && error.status !== 501;
  }
  return error instanceof TypeError;
}
__name(isTransientError, "isTransientError");
function signInRequiredMessage(entry) {
  const provider = "url" in entry.config ? entry.config.auth?.provider : void 0;
  return `MCP server "${entry.name}" requires sign-in. Run ${provider ? `/login ${provider}` : "/mcp"} to sign in.`;
}
__name(signInRequiredMessage, "signInRequiredMessage");
function usesOAuth(entry) {
  const { config } = entry;
  if (!("url" in config) || config.auth)
    return false;
  return !Object.keys(config.headers ?? {}).some((header) => header.toLowerCase() === "authorization");
}
__name(usesOAuth, "usesOAuth");
function expandHome(value) {
  if (value === "~")
    return homedir();
  if (value.startsWith("~/") || process.platform === "win32" && value.startsWith("~\\")) {
    return join2(homedir(), value.slice(2));
  }
  return value;
}
__name(expandHome, "expandHome");
function createDefaultTransport(entry, cwd, authProvider) {
  const { config, name } = entry;
  if ("url" in config) {
    return new StreamableHttpTransport({
      url: config.url,
      headers: resolveHeadersOrThrow(config.headers, `MCP server "${name}"`),
      authProvider
    });
  }
  const env = {};
  for (const [key, value] of Object.entries(config.env ?? {})) {
    env[key] = resolveConfigValueOrThrow(value, `MCP server "${name}" env "${key}"`);
  }
  return new StdioTransport({
    command: expandHome(config.command),
    args: config.args?.map(expandHome),
    cwd: resolve(cwd, expandHome(config.cwd ?? ".")),
    env,
    stderr: "pipe"
  });
}
__name(createDefaultTransport, "createDefaultTransport");
async function withoutTemplates(list, empty) {
  try {
    return await list();
  } catch (error) {
    if (error instanceof McpError && error.code === JSON_RPC_ERROR_CODES.methodNotFound)
      return empty;
    throw error;
  }
}
__name(withoutTemplates, "withoutTemplates");
function listTemplates(client, options = {}) {
  return withoutTemplates(() => client.listResourceTemplates(options), []);
}
__name(listTemplates, "listTemplates");
var McpServerConnection = class {
  static {
    __name(this, "McpServerConnection");
  }
  entry;
  state = "connecting";
  error;
  tools = [];
  /**
   * Whether the server offers resources, which the resource tools list and read on demand. They are
   * not listed at connect: listing every resource can take long (a large drive, say).
   */
  hasResources = false;
  /** Server instructions from `initialize`, describing its tools as a group. */
  instructions;
  /** Last OAuth challenge from the server; sign-in uses its resource metadata URL and scope. */
  challenge;
  client;
  opening;
  /** Aborted by `close()`; cancels a connect in progress, including the wait between retries. */
  shutdown = new AbortController();
  /** Stderr of the last stdio server that failed to connect. */
  stderrTail;
  cwd;
  createTransport;
  authProvider;
  onTools;
  onChange;
  log;
  constructor(options) {
    this.entry = options.entry;
    this.cwd = options.cwd;
    this.createTransport = options.createTransport;
    this.onTools = options.onTools;
    this.onChange = options.onChange;
    this.log = options.log;
    const url = this.oauthUrl;
    const provider = "url" in this.entry.config ? this.entry.config.auth?.provider : void 0;
    this.authProvider = url ? createMcpAuthProvider({
      serverUrl: url,
      store: options.credentials.forServer(this.entry.name, url),
      settings: /* @__PURE__ */ __name(() => this.oauthSettings(), "settings"),
      onChallenge: /* @__PURE__ */ __name((challenge) => {
        this.challenge = challenge;
      }, "onChallenge")
    }) : provider ? (
      // Read on every request, so the provider's refreshes apply; MCP stores no copy.
      { token: /* @__PURE__ */ __name(async () => options.providerToken?.(provider), "token"), settled: /* @__PURE__ */ __name(async () => {
      }, "settled") }
    ) : void 0;
  }
  get name() {
    return this.entry.name;
  }
  get closed() {
    return this.shutdown.signal.aborted;
  }
  get timeoutMs() {
    return (this.entry.config.timeout ?? DEFAULT_TIMEOUT_SECONDS) * 1e3;
  }
  /** Server URL when the server authenticates with OAuth. */
  get oauthUrl() {
    return usesOAuth(this.entry) && "url" in this.entry.config ? this.entry.config.url : void 0;
  }
  oauthSettings() {
    const oauth = "url" in this.entry.config ? this.entry.config.oauth : void 0;
    if (!oauth)
      return {};
    return {
      clientId: oauth.clientId,
      clientSecret: oauth.clientSecret === void 0 ? void 0 : resolveConfigValueOrThrow(oauth.clientSecret, `MCP server "${this.entry.name}" oauth.clientSecret`),
      callbackPort: oauth.callbackPort,
      callbackUrl: oauth.callbackUrl,
      scope: oauth.scope,
      clientName: oauth.clientName,
      clientRegistration: oauth.clientRegistration,
      authServerMetadataUrl: oauth.authServerMetadataUrl ? new URL(oauth.authServerMetadataUrl) : void 0
    };
  }
  getClient() {
    if (this.closed)
      return Promise.reject(new Error(`MCP server "${this.entry.name}" is shut down`));
    if (this.client?.connectionState === "connected")
      return Promise.resolve(this.client);
    this.opening ??= this.open().finally(() => {
      this.opening = void 0;
    });
    return this.opening;
  }
  callTool(name, args, options) {
    return this.withClient((client) => client.callTool(name, args, options));
  }
  readResource(uri, options) {
    return this.withClient((client) => client.readResource(uri, options), true);
  }
  resourcesPage(cursor, options) {
    return this.withClient((client) => client.listResourcesPage(cursor, options), true);
  }
  resourceTemplatesPage(cursor, options) {
    return this.withClient((client) => withoutTemplates(() => client.listResourceTemplatesPage(cursor, options), { resourceTemplates: [] }), true);
  }
  allResources(options) {
    return this.withClient((client) => client.listResources(options), true);
  }
  allResourceTemplates(options) {
    return this.withClient((client) => listTemplates(client, options), true);
  }
  /**
   * Run a request, reconnecting when needed. `readOnly` requests are retried once after a transient
   * HTTP error; tool calls are not, since they may have run.
   */
  async withClient(run, readOnly = false) {
    for (let attempt = 1; ; attempt++) {
      const client = await this.getClient();
      try {
        return await run(client);
      } catch (error) {
        if (readOnly && attempt === 1 && error instanceof McpHttpError && isTransientError(error)) {
          await new Promise((resolve2) => setTimeout(resolve2, CONNECT_RETRY_DELAYS_MS[0]));
          continue;
        }
        if (error instanceof McpSessionExpiredError && attempt === 1) {
          if (this.client === client)
            this.client = void 0;
          continue;
        }
        if (!this.needsSignIn(error))
          throw error;
        await this.dropClient(client);
        this.markNeedsAuth();
        throw new Error(signInRequiredMessage(this.entry));
      }
    }
  }
  /** Connect again with fresh credentials, for example after signing in. */
  async reconnect() {
    await this.opening?.catch(() => void 0);
    if (this.client)
      await this.dropClient(this.client);
    await this.getClient();
  }
  /** Disconnect after the stored credentials were removed. */
  async signOut() {
    await this.opening?.catch(() => void 0);
    if (this.client)
      await this.dropClient(this.client);
    if (!this.closed)
      this.markNeedsAuth();
  }
  /** OAuth servers that still reject the request after a refresh need the user to sign in again. */
  needsSignIn(error) {
    return error instanceof McpOAuthAuthorizationRequiredError || this.authProvider !== void 0 && error instanceof McpAuthRequiredError;
  }
  markNeedsAuth() {
    this.state = "needs-auth";
    this.error = void 0;
    this.changed();
  }
  changed() {
    this.onChange?.(this);
  }
  async dropClient(client) {
    if (this.client === client)
      this.client = void 0;
    await client.close().catch(() => void 0);
  }
  async open() {
    this.state = "connecting";
    this.changed();
    const retries = "url" in this.entry.config ? CONNECT_RETRY_DELAYS_MS : [];
    for (let attempt = 0; ; attempt++) {
      this.stderrTail = void 0;
      try {
        return await this.connectOnce();
      } catch (error) {
        const delay = retries[attempt];
        if (this.closed || delay === void 0 || !isTransientError(error)) {
          throw this.connectFailed(error);
        }
        await sleep(delay, void 0, { signal: this.shutdown.signal }).catch(() => void 0);
        if (this.closed)
          throw this.connectFailed(error);
      }
    }
  }
  async connectOnce() {
    const client = new McpClient({
      name: "pi",
      version: VERSION,
      requestTimeoutMs: this.timeoutMs,
      roots: [{ uri: pathToFileURL(this.cwd).href, name: basename(this.cwd) }]
    });
    const log = this.log;
    if (log)
      client.onNotification("notifications/message", (params) => log.write(this.entry.name, params));
    let closing;
    const closeClient = /* @__PURE__ */ __name(() => {
      closing ??= client.close().catch(() => void 0);
      return closing;
    }, "closeClient");
    this.shutdown.signal.addEventListener("abort", closeClient, { once: true });
    let transport;
    try {
      transport = this.createTransport(this.entry, this.cwd, this.authProvider);
      await client.connect(transport);
      client.onNotification("notifications/tools/list_changed", () => {
        void this.refreshTools(client);
      });
      const stdio = transport instanceof StdioTransport ? transport : void 0;
      client.onClose(() => this.handleClientClose(client, stdio));
      const tools = client.serverCapabilities?.tools ? await client.listTools() : [];
      if (this.closed)
        throw new Error("shut down while connecting");
      if (client.connectionState !== "connected")
        throw new Error("connection closed during setup");
      this.client = client;
      this.tools = tools;
      this.hasResources = client.serverCapabilities?.resources !== void 0;
      this.instructions = client.instructions?.trim() || void 0;
      this.state = "connected";
      this.error = void 0;
      this.onTools(this);
      this.changed();
      return client;
    } catch (error) {
      await closeClient();
      if (transport instanceof StdioTransport) {
        this.stderrTail = sliceTail(transport.stderr.trim(), STDERR_TAIL_CHARS) || void 0;
      }
      throw error;
    } finally {
      this.shutdown.signal.removeEventListener("abort", closeClient);
    }
  }
  connectFailed(error) {
    if (this.needsSignIn(error) && !this.closed) {
      this.markNeedsAuth();
      return new Error(signInRequiredMessage(this.entry));
    }
    this.state = this.closed ? "closed" : "failed";
    this.error = this.stderrTail ? `${errorMessage(error)}
${this.stderrTail}` : errorMessage(error);
    this.changed();
    return new Error(`MCP server "${this.entry.name}" failed to connect: ${this.error}`);
  }
  /** The transport dropped. The next call reconnects; until then the status shows why. */
  handleClientClose(client, stdio) {
    if (this.client !== client || this.closed)
      return;
    this.client = void 0;
    this.state = "disconnected";
    const stderr = stdio ? sliceTail(stdio.stderr.trim(), STDERR_TAIL_CHARS) : void 0;
    this.error = stderr ? `Connection closed
${stderr}` : "Connection closed";
    this.changed();
  }
  async refreshTools(client) {
    try {
      const tools = await client.listTools();
      if (this.client !== client || this.closed)
        return;
      this.tools = tools;
      this.onTools(this);
    } catch (error) {
      this.error = `Failed to refresh tools: ${errorMessage(error)}`;
    }
    this.changed();
  }
  /** Resolves once no transport of this server is open, including one that was still connecting. */
  async close() {
    this.shutdown.abort();
    this.state = "closed";
    this.changed();
    const client = this.client;
    this.client = void 0;
    await Promise.all([client?.close().catch(() => void 0), this.opening?.catch(() => void 0)]);
    await this.authProvider?.settled();
  }
};

export {
  McpOAuthCredentialStore,
  signInMcpServer,
  McpServerLog,
  createDefaultTransport,
  McpServerConnection,
  McpSignInCancelledError
};
