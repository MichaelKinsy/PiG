import {
  getShellConfig
} from "./chunk-EDGTPAH6.js";
import {
  getAgentDir,
  getFileRevision,
  normalizePath,
  stripBom
} from "./chunk-H7ICR3WT.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/core/mcp-servers.js
var MCP_EXPOSURES = ["codemode", "deferred", "direct", "hidden"];
var MCP_EXPOSURE_ALIASES = { "codemode-deferred": "codemode" };
var LOOPBACK_HOSTS = ["localhost", "127.0.0.1", "[::1]"];
function isLoopbackRedirectUri(value) {
  if (!URL.canParse(value))
    return false;
  const url = new URL(value);
  return url.protocol === "http:" && LOOPBACK_HOSTS.includes(url.hostname) && url.search === "" && url.hash === "";
}
__name(isLoopbackRedirectUri, "isLoopbackRedirectUri");
var SERVER_NAME = /^[A-Za-z0-9_-]+$/;
function mcpNamespace(server) {
  return `mcp__${server.replace(/-/g, "_")}`;
}
__name(mcpNamespace, "mcpNamespace");
function isRecord(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
__name(isRecord, "isRecord");
function isStringRecord(value) {
  return isRecord(value) && Object.values(value).every((entry) => typeof entry === "string");
}
__name(isStringRecord, "isStringRecord");
function validateOAuth(value) {
  if (value === void 0)
    return void 0;
  if (!isRecord(value))
    return "oauth must be an object";
  if (value.clientId !== void 0 && typeof value.clientId !== "string")
    return "oauth.clientId must be a string";
  if (value.clientSecret !== void 0 && typeof value.clientSecret !== "string") {
    return "oauth.clientSecret must be a string";
  }
  const port = value.callbackPort;
  if (port !== void 0 && (typeof port !== "number" || !Number.isInteger(port) || port < 1 || port > 65535)) {
    return "oauth.callbackPort must be a port number";
  }
  if (value.callbackUrl !== void 0) {
    if (typeof value.callbackUrl !== "string" || !isLoopbackRedirectUri(value.callbackUrl)) {
      return "oauth.callbackUrl must be an http URI on localhost, 127.0.0.1, or [::1] without query or fragment";
    }
    const urlPort = new URL(value.callbackUrl).port;
    if (urlPort && port !== void 0 && Number(urlPort) !== port) {
      return "oauth.callbackUrl and oauth.callbackPort name different ports";
    }
  }
  if (value.scope !== void 0 && typeof value.scope !== "string")
    return "oauth.scope must be a string";
  if (value.clientName !== void 0 && (typeof value.clientName !== "string" || !value.clientName.trim())) {
    return "oauth.clientName must be a non-empty string";
  }
  if (value.clientRegistration !== void 0 && value.clientRegistration !== "dcr") {
    if (value.clientRegistration !== "cimd")
      return 'oauth.clientRegistration must be "dcr" or "cimd"';
    if (value.clientId !== void 0 || value.clientName !== void 0) {
      return 'oauth.clientRegistration "cimd" cannot be combined with oauth.clientId or oauth.clientName';
    }
    const callback = typeof value.callbackUrl === "string" ? new URL(value.callbackUrl) : void 0;
    if (callback && (callback.hostname === "[::1]" || callback.pathname !== "/callback")) {
      return 'oauth.clientRegistration "cimd" requires oauth.callbackUrl on localhost or 127.0.0.1 with path /callback';
    }
  }
  const metadataUrl = value.authServerMetadataUrl;
  if (metadataUrl !== void 0) {
    const url = typeof metadataUrl === "string" && URL.canParse(metadataUrl) ? new URL(metadataUrl) : void 0;
    if (!url || !(url.protocol === "https:" || url.protocol === "http:" && LOOPBACK_HOSTS.includes(url.hostname))) {
      return "oauth.authServerMetadataUrl must be an https URL, or http on localhost, 127.0.0.1, or [::1]";
    }
  }
  return void 0;
}
__name(validateOAuth, "validateOAuth");
function isExposure(value) {
  return typeof value === "string" && MCP_EXPOSURES.includes(value);
}
__name(isExposure, "isExposure");
function resolveExposureAlias(value) {
  return typeof value === "string" ? MCP_EXPOSURE_ALIASES[value] ?? value : value;
}
__name(resolveExposureAlias, "resolveExposureAlias");
function resolveExposureAliases(value) {
  const { exposure, toolExposure } = value;
  const resolved = { ...value };
  if (exposure !== void 0)
    resolved.exposure = resolveExposureAlias(exposure);
  if (isRecord(toolExposure)) {
    resolved.toolExposure = Object.fromEntries(Object.entries(toolExposure).map(([tool, entry]) => [tool, resolveExposureAlias(entry)]));
  }
  return resolved;
}
__name(resolveExposureAliases, "resolveExposureAliases");
function toolPatternRegExp(pattern) {
  const source = pattern.split("*").map((part) => part.replace(/[.+?^${}()|[\]\\]/g, "\\$&")).join(".*");
  return new RegExp(`^${source}$`);
}
__name(toolPatternRegExp, "toolPatternRegExp");
function getMcpToolExposure(config, toolName) {
  const overrides = config.toolExposure ?? {};
  const exact = overrides[toolName];
  if (exact !== void 0)
    return exact;
  for (const [pattern, exposure] of Object.entries(overrides)) {
    if (pattern.includes("*") && toolPatternRegExp(pattern).test(toolName))
      return exposure;
  }
  return config.exposure ?? "codemode";
}
__name(getMcpToolExposure, "getMcpToolExposure");
function validateMcpServerConfig(name, raw) {
  if (!SERVER_NAME.test(name))
    return `invalid server name "${name}" (use letters, digits, "_" and "-")`;
  if (!isRecord(raw))
    return `server "${name}" must be an object`;
  const value = resolveExposureAliases(raw);
  const { type, exposure, enabled, timeout, toolExposure, description } = value;
  const exposures = MCP_EXPOSURES.map((value2) => `"${value2}"`).join(", ");
  if (exposure !== void 0 && !isExposure(exposure)) {
    return `server "${name}": exposure must be one of ${exposures}`;
  }
  if (toolExposure !== void 0) {
    if (!isRecord(toolExposure))
      return `server "${name}": toolExposure must map tool names to exposures`;
    for (const [tool, value2] of Object.entries(toolExposure)) {
      if (!isExposure(value2))
        return `server "${name}": toolExposure "${tool}" must be one of ${exposures}`;
    }
  }
  if (enabled !== void 0 && typeof enabled !== "boolean")
    return `server "${name}": enabled must be a boolean`;
  if (description !== void 0 && typeof description !== "string") {
    return `server "${name}": description must be a string`;
  }
  if (timeout !== void 0 && (typeof timeout !== "number" || !(timeout > 0))) {
    return `server "${name}": timeout must be a positive number of seconds`;
  }
  if (type === "sse")
    return `server "${name}": legacy SSE transport is not supported; use the streamable HTTP URL`;
  if (typeof value.url === "string" && (type === void 0 || type === "http" || type === "streamable-http")) {
    if (!URL.canParse(value.url) || !/^https?:$/.test(new URL(value.url).protocol)) {
      return `server "${name}": url must be an http or https URL`;
    }
    if (value.headers !== void 0 && !isStringRecord(value.headers)) {
      return `server "${name}": headers must map names to strings`;
    }
    const oauthError = validateOAuth(value.oauth);
    if (oauthError)
      return `server "${name}": ${oauthError}`;
    if (value.auth !== void 0) {
      if (!isRecord(value.auth) || typeof value.auth.provider !== "string" || !value.auth.provider) {
        return `server "${name}": auth.provider must be a provider name`;
      }
      const url = new URL(value.url);
      if (url.protocol !== "https:" && !LOOPBACK_HOSTS.includes(url.hostname)) {
        return `server "${name}": auth requires an https URL, or http on localhost, 127.0.0.1, or [::1]`;
      }
    }
    return value;
  }
  if (typeof value.command === "string" && (type === void 0 || type === "stdio")) {
    if (value.args !== void 0 && !(Array.isArray(value.args) && value.args.every((arg) => typeof arg === "string"))) {
      return `server "${name}": args must be an array of strings`;
    }
    if (value.env !== void 0 && !isStringRecord(value.env))
      return `server "${name}": env must map names to strings`;
    if (value.cwd !== void 0 && typeof value.cwd !== "string")
      return `server "${name}": cwd must be a string`;
    return value;
  }
  return `server "${name}" needs either "command" (stdio) or "url" (streamable HTTP)`;
}
__name(validateMcpServerConfig, "validateMcpServerConfig");
var McpServerRegistry = class {
  static {
    __name(this, "McpServerRegistry");
  }
  servers = /* @__PURE__ */ new Map();
  changeListener;
  /** Register or replace a server. The caller checks ownership. */
  register(server) {
    this.servers.set(server.name, server);
    this.changeListener?.();
  }
  /** Remove a server registered by `extensionPath`. Servers of other extensions are left alone. */
  unregister(name, extensionPath) {
    if (this.servers.get(name)?.extensionPath !== extensionPath)
      return;
    this.servers.delete(name);
    this.changeListener?.();
  }
  get(name) {
    return this.servers.get(name);
  }
  /** Copies of the registered servers, in registration order. */
  list() {
    return [...this.servers.values()].map((server) => ({ ...server, config: structuredClone(server.config) }));
  }
  /** Called after every change. The runner sets it when it binds, to emit `mcp_servers_change`. */
  setChangeListener(listener) {
    this.changeListener = listener;
  }
};

// pi-dist/pi-coding-agent/core/resolve-config-value.js
import { execSync, spawnSync } from "child_process";
var commandResultCache = /* @__PURE__ */ new Map();
var ENV_VAR_NAME_RE = /^[A-Za-z_][A-Za-z0-9_]*$/;
var ENV_VAR_NAME_PREFIX_RE = /^[A-Za-z_][A-Za-z0-9_]*/;
function appendLiteral(parts, value) {
  if (!value)
    return;
  const previousPart = parts[parts.length - 1];
  if (previousPart?.type === "literal") {
    previousPart.value += value;
    return;
  }
  parts.push({ type: "literal", value });
}
__name(appendLiteral, "appendLiteral");
function parseConfigValueTemplate(config) {
  const parts = [];
  let index = 0;
  while (index < config.length) {
    const dollarIndex = config.indexOf("$", index);
    if (dollarIndex < 0) {
      appendLiteral(parts, config.slice(index));
      break;
    }
    appendLiteral(parts, config.slice(index, dollarIndex));
    const nextChar = config[dollarIndex + 1];
    if (nextChar === "$" || nextChar === "!") {
      appendLiteral(parts, nextChar);
      index = dollarIndex + 2;
      continue;
    }
    if (nextChar === "{") {
      const endIndex = config.indexOf("}", dollarIndex + 2);
      if (endIndex < 0) {
        appendLiteral(parts, "$");
        index = dollarIndex + 1;
        continue;
      }
      const name = config.slice(dollarIndex + 2, endIndex);
      if (ENV_VAR_NAME_RE.test(name)) {
        parts.push({ type: "env", name });
      } else {
        appendLiteral(parts, config.slice(dollarIndex, endIndex + 1));
      }
      index = endIndex + 1;
      continue;
    }
    const match = config.slice(dollarIndex + 1).match(ENV_VAR_NAME_PREFIX_RE);
    if (match) {
      parts.push({ type: "env", name: match[0] });
      index = dollarIndex + 1 + match[0].length;
      continue;
    }
    appendLiteral(parts, "$");
    index = dollarIndex + 1;
  }
  return parts;
}
__name(parseConfigValueTemplate, "parseConfigValueTemplate");
function parseConfigValueReference(config) {
  if (config.startsWith("!")) {
    return { type: "command", config };
  }
  return { type: "template", parts: parseConfigValueTemplate(config) };
}
__name(parseConfigValueReference, "parseConfigValueReference");
function resolveEnvConfigValue(name, env) {
  return env?.[name] || process.env[name] || void 0;
}
__name(resolveEnvConfigValue, "resolveEnvConfigValue");
function getTemplateEnvVarNames(parts) {
  const names = [];
  for (const part of parts) {
    if (part.type !== "env" || names.includes(part.name))
      continue;
    names.push(part.name);
  }
  return names;
}
__name(getTemplateEnvVarNames, "getTemplateEnvVarNames");
function resolveTemplate(parts, env) {
  let resolved = "";
  for (const part of parts) {
    if (part.type === "literal") {
      resolved += part.value;
      continue;
    }
    const envValue = resolveEnvConfigValue(part.name, env);
    if (envValue === void 0)
      return void 0;
    resolved += envValue;
  }
  return resolved;
}
__name(resolveTemplate, "resolveTemplate");
function getConfigValueEnvVarNames(config) {
  const reference = parseConfigValueReference(config);
  return reference.type === "template" ? getTemplateEnvVarNames(reference.parts) : [];
}
__name(getConfigValueEnvVarNames, "getConfigValueEnvVarNames");
function getMissingConfigValueEnvVarNames(config, env) {
  return getConfigValueEnvVarNames(config).filter((name) => resolveEnvConfigValue(name, env) === void 0);
}
__name(getMissingConfigValueEnvVarNames, "getMissingConfigValueEnvVarNames");
function isCommandConfigValue(config) {
  return parseConfigValueReference(config).type === "command";
}
__name(isCommandConfigValue, "isCommandConfigValue");
function isConfigValueConfigured(config, env) {
  return getMissingConfigValueEnvVarNames(config, env).length === 0;
}
__name(isConfigValueConfigured, "isConfigValueConfigured");
function resolveConfigValue(config, env) {
  const reference = parseConfigValueReference(config);
  if (reference.type === "command") {
    return executeCommand(reference.config);
  }
  return resolveTemplate(reference.parts, env);
}
__name(resolveConfigValue, "resolveConfigValue");
function executeWithConfiguredShell(command) {
  try {
    const { shell, args, commandTransport } = getShellConfig();
    const commandFromStdin = commandTransport === "stdin";
    const result = spawnSync(shell, commandFromStdin ? args : [...args, command], {
      encoding: "utf-8",
      input: commandFromStdin ? command : void 0,
      timeout: 1e4,
      stdio: [commandFromStdin ? "pipe" : "ignore", "pipe", "ignore"],
      shell: false,
      windowsHide: true
    });
    if (result.error) {
      const error = result.error;
      if (error.code === "ENOENT") {
        return { executed: false, value: void 0 };
      }
      return { executed: true, value: void 0 };
    }
    if (result.status !== 0) {
      return { executed: true, value: void 0 };
    }
    const value = (result.stdout ?? "").trim();
    return { executed: true, value: value || void 0 };
  } catch {
    return { executed: false, value: void 0 };
  }
}
__name(executeWithConfiguredShell, "executeWithConfiguredShell");
function executeWithDefaultShell(command) {
  try {
    const output = execSync(command, {
      encoding: "utf-8",
      timeout: 1e4,
      stdio: ["ignore", "pipe", "ignore"]
    });
    return output.trim() || void 0;
  } catch {
    return void 0;
  }
}
__name(executeWithDefaultShell, "executeWithDefaultShell");
function executeCommandUncached(commandConfig) {
  const command = commandConfig.slice(1);
  return process.platform === "win32" ? (() => {
    const configuredResult = executeWithConfiguredShell(command);
    return configuredResult.executed ? configuredResult.value : executeWithDefaultShell(command);
  })() : executeWithDefaultShell(command);
}
__name(executeCommandUncached, "executeCommandUncached");
function executeCommand(commandConfig) {
  if (commandResultCache.has(commandConfig)) {
    return commandResultCache.get(commandConfig);
  }
  const result = executeCommandUncached(commandConfig);
  commandResultCache.set(commandConfig, result);
  return result;
}
__name(executeCommand, "executeCommand");
function resolveConfigValueUncached(config, env) {
  const reference = parseConfigValueReference(config);
  if (reference.type === "command") {
    return executeCommandUncached(reference.config);
  }
  return resolveTemplate(reference.parts, env);
}
__name(resolveConfigValueUncached, "resolveConfigValueUncached");
function resolveConfigValueOrThrow(config, description, env) {
  const resolvedValue = resolveConfigValueUncached(config, env);
  if (resolvedValue !== void 0) {
    return resolvedValue;
  }
  const reference = parseConfigValueReference(config);
  if (reference.type === "command") {
    throw new Error(`Failed to resolve ${description} from shell command: ${reference.config.slice(1)}`);
  }
  if (reference.type === "template") {
    const missingEnvVars = getMissingConfigValueEnvVarNames(config, env);
    if (missingEnvVars.length === 1) {
      throw new Error(`Failed to resolve ${description} from environment variable: ${missingEnvVars[0]}`);
    }
    if (missingEnvVars.length > 1) {
      throw new Error(`Failed to resolve ${description} from environment variables: ${missingEnvVars.join(", ")}`);
    }
  }
  throw new Error(`Failed to resolve ${description}`);
}
__name(resolveConfigValueOrThrow, "resolveConfigValueOrThrow");
function resolveHeadersOrThrow(headers, description, env) {
  if (!headers)
    return void 0;
  const resolved = {};
  for (const [key, value] of Object.entries(headers)) {
    resolved[key] = resolveConfigValueOrThrow(value, `${description} header "${key}"`, env);
  }
  return Object.keys(resolved).length > 0 ? resolved : void 0;
}
__name(resolveHeadersOrThrow, "resolveHeadersOrThrow");

// pi-dist/pi-coding-agent/core/auth-storage.js
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "fs";
import { dirname, join } from "path";
import lockfile from "../../../proper-lockfile.mjs";
import { setTimeout as sleep } from "timers/promises";

// pi-dist/pi-coding-agent/utils/abort.js
function abortReason(signal) {
  if (signal.reason !== void 0)
    return signal.reason;
  const error = new Error("The operation was aborted");
  error.name = "AbortError";
  return error;
}
__name(abortReason, "abortReason");
function operationSignal(signal) {
  return signal ?? new AbortController().signal;
}
__name(operationSignal, "operationSignal");
function raceWithAbortSignal(operation, signal) {
  if (!signal)
    return operation;
  if (signal.aborted) {
    void operation.catch(() => {
    });
    return Promise.reject(abortReason(signal));
  }
  return new Promise((resolve, reject) => {
    let settled = false;
    const cleanup = /* @__PURE__ */ __name(() => signal.removeEventListener("abort", onAbort), "cleanup");
    const onAbort = /* @__PURE__ */ __name(() => {
      if (settled)
        return;
      settled = true;
      cleanup();
      reject(abortReason(signal));
    }, "onAbort");
    signal.addEventListener("abort", onAbort, { once: true });
    void operation.then((value) => {
      if (settled)
        return;
      settled = true;
      cleanup();
      resolve(value);
    }, (error) => {
      if (settled)
        return;
      settled = true;
      cleanup();
      reject(error);
    });
    if (signal.aborted)
      onAbort();
  });
}
__name(raceWithAbortSignal, "raceWithAbortSignal");

// pi-dist/pi-coding-agent/core/auth-storage.js
var AUTH_FILE_WRITE_OPTIONS = { encoding: "utf-8", mode: 384 };
var sharedAuthFileReadState;
var FileAuthStorageBackend = class {
  static {
    __name(this, "FileAuthStorageBackend");
  }
  authPath;
  constructor(authPath = join(getAgentDir(), "auth.json")) {
    this.authPath = normalizePath(authPath);
  }
  ensureParentDir() {
    const dir = dirname(this.authPath);
    if (!existsSync(dir)) {
      mkdirSync(dir, { recursive: true, mode: 448 });
    }
  }
  ensureFileExists() {
    if (!existsSync(this.authPath)) {
      writeFileSync(this.authPath, "{}", AUTH_FILE_WRITE_OPTIONS);
    }
  }
  acquireLockSyncWithRetry(path) {
    const maxAttempts = 10;
    const delayMs = 20;
    let lastError;
    for (let attempt = 1; attempt <= maxAttempts; attempt++) {
      try {
        return lockfile.lockSync(path, { realpath: false });
      } catch (error) {
        const code = typeof error === "object" && error !== null && "code" in error ? String(error.code) : void 0;
        if (code !== "ELOCKED" || attempt === maxAttempts) {
          throw error;
        }
        lastError = error;
        const start = Date.now();
        while (Date.now() - start < delayMs) {
        }
      }
    }
    throw lastError ?? new Error("Failed to acquire auth storage lock");
  }
  withLock(fn) {
    this.ensureParentDir();
    this.ensureFileExists();
    let release;
    try {
      release = this.acquireLockSyncWithRetry(this.authPath);
      const current = existsSync(this.authPath) ? readFileSync(this.authPath, "utf-8") : void 0;
      const { result, next } = fn(current);
      if (next !== void 0) {
        writeFileSync(this.authPath, next, AUTH_FILE_WRITE_OPTIONS);
      }
      return result;
    } finally {
      if (release) {
        release();
      }
    }
  }
  async acquireLockAsync(signal, onCompromised) {
    const staleMs = 3e4;
    const maxDelayMs = 2e3;
    const deadline = Date.now() + staleMs;
    let retry = 0;
    while (true) {
      signal?.throwIfAborted();
      let release;
      try {
        release = await lockfile.lock(this.authPath, {
          realpath: false,
          retries: 0,
          stale: staleMs,
          onCompromised
        });
      } catch (error) {
        signal?.throwIfAborted();
        const code = typeof error === "object" && error !== null && "code" in error ? String(error.code) : void 0;
        const remainingMs = deadline - Date.now();
        if (code !== "ELOCKED" || remainingMs <= 0)
          throw error;
        const baseDelayMs = Math.min(10 * 2 ** retry, maxDelayMs / 2);
        retry++;
        const delayMs = Math.min(Math.round(baseDelayMs * (1 + Math.random())), remainingMs);
        if (signal)
          await sleep(delayMs, void 0, { signal });
        else
          await sleep(delayMs);
        continue;
      }
      if (signal?.aborted) {
        await release();
        signal.throwIfAborted();
      }
      return release;
    }
  }
  async withLockAsync(fn, options) {
    options?.signal?.throwIfAborted();
    this.ensureParentDir();
    this.ensureFileExists();
    let release;
    let lockCompromised = false;
    let lockCompromisedError;
    const throwIfCompromised = /* @__PURE__ */ __name(() => {
      if (lockCompromised) {
        throw lockCompromisedError ?? new Error("Auth storage lock was compromised");
      }
    }, "throwIfCompromised");
    try {
      release = await this.acquireLockAsync(options?.signal, (error) => {
        lockCompromised = true;
        lockCompromisedError = error;
      });
      throwIfCompromised();
      options?.signal?.throwIfAborted();
      const current = existsSync(this.authPath) ? readFileSync(this.authPath, "utf-8") : void 0;
      const { result, next } = await fn(current);
      throwIfCompromised();
      options?.signal?.throwIfAborted();
      if (next !== void 0) {
        writeFileSync(this.authPath, next, AUTH_FILE_WRITE_OPTIONS);
      }
      throwIfCompromised();
      return result;
    } finally {
      if (release) {
        try {
          await release();
        } catch {
        }
      }
    }
  }
};
var ReadOnlyAuthStorage = class {
  static {
    __name(this, "ReadOnlyAuthStorage");
  }
  authPath;
  data;
  constructor(authPath = join(getAgentDir(), "auth.json")) {
    this.authPath = normalizePath(authPath);
  }
  load() {
    if (this.data)
      return this.data;
    let parsed;
    try {
      parsed = JSON.parse(stripBom(readFileSync(this.authPath, "utf-8")));
    } catch (error) {
      if (error.code === "ENOENT") {
        this.data = {};
        return this.data;
      }
      throw new Error(`Failed to read auth.json: ${error instanceof Error ? error.message : String(error)}`);
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      throw new Error("Invalid auth.json: expected an object");
    }
    for (const [providerId, credential] of Object.entries(parsed)) {
      if (typeof credential !== "object" || credential === null || Array.isArray(credential)) {
        throw new Error(`Invalid auth.json credential for provider "${providerId}"`);
      }
      const value = credential;
      if (value.type === "api_key") {
        const validKey = value.key === void 0 || typeof value.key === "string";
        const validEnv = value.env === void 0 || typeof value.env === "object" && value.env !== null && !Array.isArray(value.env) && Object.values(value.env).every((entry) => typeof entry === "string");
        if (validKey && validEnv)
          continue;
      } else if (value.type === "oauth" && typeof value.access === "string" && typeof value.refresh === "string" && typeof value.expires === "number" && Number.isFinite(value.expires)) {
        continue;
      }
      throw new Error(`Invalid auth.json credential for provider "${providerId}"`);
    }
    this.data = parsed;
    return this.data;
  }
  async read(providerId, options) {
    options?.signal?.throwIfAborted();
    const credential = this.load()[providerId];
    options?.signal?.throwIfAborted();
    if (!credential)
      return void 0;
    if (credential.type !== "api_key" || !credential.key || isCommandConfigValue(credential.key)) {
      return structuredClone(credential);
    }
    return { ...credential, key: resolveConfigValue(credential.key, credential.env) };
  }
  async list(options) {
    options?.signal?.throwIfAborted();
    const credentials = Object.entries(this.load()).map(([providerId, credential]) => ({
      providerId,
      type: credential.type
    }));
    options?.signal?.throwIfAborted();
    return credentials;
  }
  async modify(_providerId, _fn, _options) {
    throw new Error("Read-only credential storage cannot modify auth.json");
  }
  async delete(_providerId, _options) {
    throw new Error("Read-only credential storage cannot modify auth.json");
  }
};
var InMemoryAuthStorageBackend = class {
  static {
    __name(this, "InMemoryAuthStorageBackend");
  }
  value;
  asyncChain = Promise.resolve();
  withLock(fn) {
    const { result, next } = fn(this.value);
    if (next !== void 0) {
      this.value = next;
    }
    return result;
  }
  withLockAsync(fn, options) {
    const previous = this.asyncChain;
    const operation = (async () => {
      await previous.catch(() => {
      });
      options?.signal?.throwIfAborted();
      const { result, next } = await fn(this.value);
      options?.signal?.throwIfAborted();
      if (next !== void 0) {
        this.value = next;
      }
      return result;
    })();
    this.asyncChain = operation.catch(() => {
    });
    return raceWithAbortSignal(operation, options?.signal);
  }
};
var AuthStorage = class _AuthStorage {
  static {
    __name(this, "AuthStorage");
  }
  storage;
  authPath;
  readState;
  constructor(storage, authPath) {
    this.storage = storage;
    this.authPath = authPath;
    this.readState = authPath && sharedAuthFileReadState?.authPath === authPath ? sharedAuthFileReadState.readState : { data: {} };
    if (authPath && !sharedAuthFileReadState) {
      sharedAuthFileReadState = { authPath, readState: this.readState };
    }
    if (authPath) {
      const revision = getFileRevision(authPath);
      if (revision !== void 0 && revision === this.readState.revision)
        return;
    }
    this.reload();
  }
  static create(authPath = join(getAgentDir(), "auth.json")) {
    const normalizedAuthPath = normalizePath(authPath);
    return new _AuthStorage(new FileAuthStorageBackend(normalizedAuthPath), normalizedAuthPath);
  }
  static fromStorage(storage) {
    return new _AuthStorage(storage);
  }
  static inMemory(data = {}) {
    const storage = new InMemoryAuthStorageBackend();
    storage.withLock(() => ({ result: void 0, next: JSON.stringify(data, null, 2) }));
    return _AuthStorage.fromStorage(storage);
  }
  parseStorageData(content) {
    if (!content) {
      return {};
    }
    return JSON.parse(stripBom(content));
  }
  updateReadState(data, revision) {
    this.readState.data = data;
    this.readState.revision = revision;
  }
  /**
   * Reload credentials from storage.
   */
  reload() {
    let content;
    let revision;
    try {
      this.storage.withLock((current) => {
        content = current;
        revision = this.authPath ? getFileRevision(this.authPath) : void 0;
        return { result: void 0 };
      });
      this.updateReadState(this.parseStorageData(content), revision);
    } catch {
    }
  }
  async reloadFromStorageAsync(options) {
    return this.storage.withLockAsync(async (content) => {
      const currentData = this.parseStorageData(content);
      const revision = this.authPath ? getFileRevision(this.authPath) : void 0;
      this.updateReadState(currentData, revision);
      return { result: currentData };
    }, options);
  }
  async readLatestData(options) {
    options?.signal?.throwIfAborted();
    if (!this.authPath) {
      const reload2 = this.reloadFromStorageAsync(options);
      return options?.signal ? reload2 : reload2.catch(() => this.readState.data);
    }
    const revision = getFileRevision(this.authPath);
    if (revision !== void 0 && revision === this.readState.revision)
      return this.readState.data;
    if (!this.readState.reload) {
      const controller = new AbortController();
      const reload2 = {
        controller,
        promise: this.reloadFromStorageAsync({ signal: controller.signal }),
        readers: 0
      };
      this.readState.reload = reload2;
      void reload2.promise.then(() => {
        if (this.readState.reload === reload2)
          this.readState.reload = void 0;
      }, () => {
        if (this.readState.reload === reload2)
          this.readState.reload = void 0;
      });
    }
    const reload = this.readState.reload;
    reload.readers++;
    try {
      const result = raceWithAbortSignal(reload.promise, options?.signal);
      return options?.signal ? await result : await result.catch(() => this.readState.data);
    } finally {
      reload.readers--;
      if (reload.readers === 0 && this.readState.reload === reload) {
        this.readState.reload = void 0;
        reload.controller.abort();
      }
    }
  }
  async read(provider, options) {
    const credential = (await this.readLatestData(options))[provider];
    options?.signal?.throwIfAborted();
    if (credential?.type !== "api_key")
      return credential;
    if (credential.key === void 0)
      return credential;
    return { ...credential, key: resolveConfigValue(credential.key, credential.env) };
  }
  async modify(provider, fn, options) {
    let latestData = this.readState.data;
    let revision;
    const result = await this.storage.withLockAsync(async (content) => {
      const currentData = this.parseStorageData(content);
      const next = await fn(currentData[provider]);
      if (next === void 0) {
        latestData = currentData;
        revision = this.authPath ? getFileRevision(this.authPath) : void 0;
        return { result: currentData[provider] };
      }
      const merged = { ...currentData, [provider]: next };
      latestData = merged;
      return { result: next, next: JSON.stringify(merged, null, 2) };
    }, options);
    this.updateReadState(latestData, revision);
    return result;
  }
  async delete(provider, options) {
    let latestData = this.readState.data;
    await this.storage.withLockAsync(async (content) => {
      const currentData = this.parseStorageData(content);
      delete currentData[provider];
      latestData = currentData;
      return { result: void 0, next: JSON.stringify(currentData, null, 2) };
    }, options);
    this.updateReadState(latestData);
  }
  /** List credential metadata without resolving configured key values. */
  async list(options) {
    const entries = Object.entries(await this.readLatestData(options));
    options?.signal?.throwIfAborted();
    return entries.map(([providerId, credential]) => ({ providerId, type: credential.type }));
  }
};
function readStoredCredential(providerId, authPath = join(getAgentDir(), "auth.json")) {
  try {
    const data = JSON.parse(stripBom(readFileSync(normalizePath(authPath), "utf-8")));
    return data[providerId];
  } catch {
    return void 0;
  }
}
__name(readStoredCredential, "readStoredCredential");

export {
  mcpNamespace,
  getMcpToolExposure,
  validateMcpServerConfig,
  McpServerRegistry,
  getConfigValueEnvVarNames,
  isCommandConfigValue,
  isConfigValueConfigured,
  resolveConfigValueOrThrow,
  resolveHeadersOrThrow,
  operationSignal,
  raceWithAbortSignal,
  FileAuthStorageBackend,
  ReadOnlyAuthStorage,
  AuthStorage,
  readStoredCredential
};
