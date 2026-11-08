/**
 * MCP server configuration and the servers extensions register with `pi.registerMcpServer()`.
 *
 * The core only validates and stores registrations. The MCP extension (built in, or another
 * extension that handles `mcp_servers_change`) connects them next to the servers from `mcp.json`.
 * The `mcpServers` shape itself (stdio and HTTP fields, `oauth`, `enabled`, `description`, `timeout`)
 * is pi-mcp's; this file adds pi's fields to it.
 */
import { validateMcpServerConfig as validateMcpServerFields, } from "../../pi-mcp/index.js";
/**
 * Regular expression for a tool name pattern where `*` matches any characters, as `toolExposure`,
 * `--tools`, and `--exclude-tools` accept them.
 */
function toolPatternRegExp(pattern) {
    const source = pattern
        .split("*")
        .map((part) => part.replace(/[.+?^${}()|[\]\\]/g, "\\$&"))
        .join(".*");
    return new RegExp(`^${source}$`);
}
/** Whether a tool name matches any of the entries, each an exact name or a pattern. */
export function createToolNameMatcher(entries) {
    const names = new Set(entries.filter((entry) => !entry.includes("*")));
    const patterns = entries.filter((entry) => entry.includes("*")).map(toolPatternRegExp);
    return (name) => names.has(name) || patterns.some((pattern) => pattern.test(name));
}
/** MCP resource tools, which reach every server with resources. */
export const LIST_MCP_RESOURCES_TOOL = "list_mcp_resources";
export const LIST_MCP_RESOURCE_TEMPLATES_TOOL = "list_mcp_resource_templates";
export const READ_MCP_RESOURCE_TOOL = "read_mcp_resource";
const MCP_RESOURCE_TOOLS = new Set([
    LIST_MCP_RESOURCES_TOOL,
    LIST_MCP_RESOURCE_TEMPLATES_TOOL,
    READ_MCP_RESOURCE_TOOL,
]);
/** Whether a tool comes from MCP: a server tool (`mcp__<server>__<tool>`) or a resource tool. */
export function isMcpToolName(name) {
    return name.startsWith("mcp__") || MCP_RESOURCE_TOOLS.has(name);
}
const MCP_EXPOSURES = ["codemode", "deferred", "direct", "hidden"];
/** Older exposure names, accepted in configs and replaced by their current name when validated. */
const MCP_EXPOSURE_ALIASES = { "codemode-deferred": "codemode" };
/**
 * Whether pi's Client ID Metadata Documents list a redirect URI for the loopback `callbackUrl`: they list
 * `/callback` on localhost and 127.0.0.1, on any port. Without `callbackUrl`, pi uses such a URI.
 */
export function isClientMetadataDocumentCallback(callbackUrl) {
    if (callbackUrl === undefined)
        return true;
    const url = new URL(callbackUrl);
    return url.hostname !== "[::1]" && url.pathname === "/callback";
}
function isRecord(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
function isExposure(value) {
    return typeof value === "string" && MCP_EXPOSURES.includes(value);
}
/** The exposure an alias stands for; other values are returned unchanged. */
function resolveExposureAlias(value) {
    return typeof value === "string" ? (MCP_EXPOSURE_ALIASES[value] ?? value) : value;
}
/** A copy of the server entry with exposure aliases replaced by their current names. */
function resolveExposureAliases(value) {
    const { exposure, toolExposure } = value;
    const resolved = { ...value };
    if (exposure !== undefined)
        resolved.exposure = resolveExposureAlias(exposure);
    if (isRecord(toolExposure)) {
        resolved.toolExposure = Object.fromEntries(Object.entries(toolExposure).map(([tool, entry]) => [tool, resolveExposureAlias(entry)]));
    }
    return resolved;
}
/** Exposure of one tool of a server: its `toolExposure` entry, else the server's `exposure`. */
export function getMcpToolExposure(config, toolName) {
    const overrides = config.toolExposure ?? {};
    const exact = overrides[toolName];
    if (exact !== undefined)
        return exact;
    for (const [pattern, exposure] of Object.entries(overrides)) {
        if (pattern.includes("*") && toolPatternRegExp(pattern).test(toolName))
            return exposure;
    }
    return config.exposure ?? "codemode";
}
/** Checks `exposure` and `toolExposure`, after aliases were resolved. Returns an error message. */
function checkExposures(entry) {
    const { exposure, toolExposure } = entry;
    const exposures = MCP_EXPOSURES.map((value) => `"${value}"`).join(", ");
    if (exposure !== undefined && !isExposure(exposure))
        return `exposure must be one of ${exposures}`;
    if (toolExposure !== undefined) {
        if (!isRecord(toolExposure))
            return "toolExposure must map tool names to exposures";
        for (const [tool, value] of Object.entries(toolExposure)) {
            if (!isExposure(value))
                return `toolExposure "${tool}" must be one of ${exposures}`;
        }
    }
    return undefined;
}
const LOOPBACK_HOSTS = ["localhost", "127.0.0.1", "[::1]"];
/** Checks `auth` of an HTTP server, which sends a provider's credential to `url`. Returns an error message. */
function checkAuth(auth, url) {
    if (auth === undefined)
        return undefined;
    if (!isRecord(auth) || typeof auth.provider !== "string" || !auth.provider) {
        return "auth.provider must be a provider name";
    }
    const parsed = new URL(url);
    if (parsed.protocol !== "https:" && !LOOPBACK_HOSTS.includes(parsed.hostname)) {
        return "auth requires an https URL, or http on localhost, 127.0.0.1, or [::1]";
    }
    return undefined;
}
/**
 * Validate one server entry of the `mcpServers` shape: pi-mcp's shared fields and pi's own. Returns a
 * copy of the config with exposure aliases resolved, or an error message.
 */
export function validateMcpServerConfig(name, raw) {
    const result = validateMcpServerFields(name, isRecord(raw) ? resolveExposureAliases(raw) : raw, {
        checkMetadataDocumentCallback: (callbackUrl) => isClientMetadataDocumentCallback(callbackUrl)
            ? undefined
            : 'oauth.clientRegistration "cimd" requires oauth.callbackUrl on localhost or 127.0.0.1 with path /callback',
    });
    if (result.kind === "invalid")
        return result.error;
    const error = checkExposures(result.config) ??
        (result.kind === "http" ? checkAuth(result.config.auth, result.config.url) : undefined);
    if (error)
        return `server "${name}": ${error}`;
    // The cast holds because pi's fields were checked above, which the types cannot express.
    return result.config;
}
/** Servers registered by the extensions of one runtime. */
export class McpServerRegistry {
    servers = new Map();
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
}
//# sourceMappingURL=mcp-servers.js.map