/**
 * The `mcpServers` configuration shape that MCP clients share, so configurations can be copied between
 * them: stdio servers (`command`, `args`, `env`, `cwd`), Streamable HTTP servers (`url`, `headers`,
 * `oauth`), and `enabled`, `description` and `timeout` for both.
 *
 * ```json
 * {
 *   "mcpServers": {
 *     "filesystem": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-filesystem", "."] },
 *     "docs": { "url": "https://example.com/mcp", "headers": { "Authorization": "Bearer ${DOCS_TOKEN}" } }
 *   }
 * }
 * ```
 *
 * Applications add fields of their own: {@link validateMcpServerConfig} keeps fields it does not know
 * for the caller to check afterwards.
 */
const SERVER_NAME = /^[A-Za-z0-9_-]+$/;
/**
 * Validate one entry of the `mcpServers` shape (configuration files are a trust boundary). Server
 * names are letters, digits, `_` and `-`, which tool names and namespaces are built from.
 */
export function validateMcpServerConfig(name, raw, options = {}) {
    const invalid = (error) => ({ kind: "invalid", error });
    if (!SERVER_NAME.test(name))
        return invalid(`invalid server name "${name}" (use letters, digits, "_" and "-")`);
    if (!isRecord(raw))
        return invalid(`server "${name}" must be an object`);
    const fieldsError = validateBaseFields(raw);
    if (fieldsError)
        return invalid(`server "${name}": ${fieldsError}`);
    const { type, url, command } = raw;
    if (type === "sse") {
        return invalid(`server "${name}": legacy SSE transport is not supported; use the streamable HTTP URL`);
    }
    // The casts below hold because the fields were checked one by one, which the types cannot express.
    if (typeof url === "string" && (type === undefined || type === "http" || type === "streamable-http")) {
        const error = validateHttpFields(raw, url, options);
        if (error)
            return invalid(`server "${name}": ${error}`);
        return { kind: "http", config: raw };
    }
    if (typeof command === "string" && (type === undefined || type === "stdio")) {
        const error = validateStdioFields(raw);
        if (error)
            return invalid(`server "${name}": ${error}`);
        return { kind: "stdio", config: raw };
    }
    return invalid(`server "${name}" needs either "command" (stdio) or "url" (streamable HTTP)`);
}
function isRecord(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
function validateBaseFields(entry) {
    const { enabled, timeout, description } = entry;
    if (enabled !== undefined && typeof enabled !== "boolean")
        return "enabled must be a boolean";
    if (description !== undefined && typeof description !== "string")
        return "description must be a string";
    if (timeout !== undefined && (typeof timeout !== "number" || !(timeout > 0))) {
        return "timeout must be a positive number of seconds";
    }
    return undefined;
}
function validateHttpFields(entry, url, options) {
    if (!URL.canParse(url) || !/^https?:$/.test(new URL(url).protocol))
        return "url must be an http or https URL";
    if (entry.headers !== undefined && !isStringRecord(entry.headers))
        return "headers must map names to strings";
    return validateOAuth(entry.oauth, options);
}
function validateStdioFields(entry) {
    const { args, env, cwd } = entry;
    if (args !== undefined && !(Array.isArray(args) && args.every((arg) => typeof arg === "string"))) {
        return "args must be an array of strings";
    }
    if (env !== undefined && !isStringRecord(env))
        return "env must map names to strings";
    if (cwd !== undefined && typeof cwd !== "string")
        return "cwd must be a string";
    return undefined;
}
function isStringRecord(value) {
    return isRecord(value) && Object.values(value).every((entry) => typeof entry === "string");
}
const LOOPBACK_HOSTS = ["localhost", "127.0.0.1", "[::1]"];
function validateOAuth(value, options) {
    if (value === undefined)
        return undefined;
    if (!isRecord(value))
        return "oauth must be an object";
    if (value.clientId !== undefined && typeof value.clientId !== "string")
        return "oauth.clientId must be a string";
    if (value.clientSecret !== undefined && typeof value.clientSecret !== "string") {
        return "oauth.clientSecret must be a string";
    }
    const port = value.callbackPort;
    if (port !== undefined && (typeof port !== "number" || !Number.isInteger(port) || port < 1 || port > 65535)) {
        return "oauth.callbackPort must be a port number";
    }
    if (value.callbackUrl !== undefined) {
        if (typeof value.callbackUrl !== "string" || !isLoopbackRedirectUri(value.callbackUrl)) {
            return "oauth.callbackUrl must be an http URI on localhost, 127.0.0.1, or [::1] without query or fragment";
        }
        const urlPort = new URL(value.callbackUrl).port;
        if (urlPort && port !== undefined && Number(urlPort) !== port) {
            return "oauth.callbackUrl and oauth.callbackPort name different ports";
        }
    }
    if (value.scope !== undefined && typeof value.scope !== "string")
        return "oauth.scope must be a string";
    if (value.clientName !== undefined && (typeof value.clientName !== "string" || !value.clientName.trim())) {
        return "oauth.clientName must be a non-empty string";
    }
    const registration = value.clientRegistration;
    if (registration !== undefined && registration !== "auto" && registration !== "dcr") {
        if (registration !== "cimd")
            return 'oauth.clientRegistration must be "auto", "dcr", or "cimd"';
        if (value.clientId !== undefined || value.clientName !== undefined) {
            return 'oauth.clientRegistration "cimd" cannot be combined with oauth.clientId or oauth.clientName';
        }
        if (!options.checkMetadataDocumentCallback) {
            return 'oauth.clientRegistration "cimd" is not supported: there are no Client ID Metadata Documents';
        }
        const callbackError = options.checkMetadataDocumentCallback(typeof value.callbackUrl === "string" ? value.callbackUrl : undefined);
        if (callbackError)
            return callbackError;
    }
    const metadataUrl = value.authServerMetadataUrl;
    if (metadataUrl !== undefined) {
        const url = typeof metadataUrl === "string" && URL.canParse(metadataUrl) ? new URL(metadataUrl) : undefined;
        if (!url || !(url.protocol === "https:" || (url.protocol === "http:" && LOOPBACK_HOSTS.includes(url.hostname)))) {
            return "oauth.authServerMetadataUrl must be an https URL, or http on localhost, 127.0.0.1, or [::1]";
        }
    }
    return undefined;
}
/** Whether a redirect URI can be served by a loopback callback server. */
function isLoopbackRedirectUri(value) {
    if (!URL.canParse(value))
        return false;
    const url = new URL(value);
    return url.protocol === "http:" && LOOPBACK_HOSTS.includes(url.hostname) && url.search === "" && url.hash === "";
}
/**
 * Whether an entry overrides a server defined elsewhere instead of defining one: it has no `command`,
 * `url` or `type`. Applications let a less trusted file (a project's) override a few fields, such as
 * `enabled`, of a server a trusted file defines; which fields is theirs to decide.
 */
export function isMcpServerOverride(entry) {
    return entry.command === undefined && entry.url === undefined && entry.type === undefined;
}
//# sourceMappingURL=config.js.map