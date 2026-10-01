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

export {
  mcpNamespace,
  getMcpToolExposure,
  validateMcpServerConfig,
  McpServerRegistry
};
