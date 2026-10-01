/**
 * The part of the MCP integration that talks to servers: connections, transports, and OAuth
 * sign-in. It pulls in the MCP client, so index.ts loads it through runtime.lazy.ts only when a
 * server is configured.
 */
import { homedir } from "node:os";
import { basename, join, resolve } from "node:path";
import { pathToFileURL } from "node:url";
import { JSON_RPC_ERROR_CODES, McpAuthRequiredError, McpClient, McpError, McpHttpError, McpSessionExpiredError, StdioTransport, StreamableHttpTransport, } from "../../../pi-mcp/index.js";
import { McpOAuthAuthorizationRequiredError } from "../../../pi-mcp/oauth/index.js";
import { VERSION } from "../../config.js";
import { resolveConfigValueOrThrow, resolveHeadersOrThrow } from "../../core/resolve-config-value.js";
import { createMcpAuthProvider, } from "./oauth.js";
import { isMcpAppResource } from "./resources.js";
export { McpServerLog } from "./log.js";
export { McpOAuthCredentialStore, McpSignInCancelledError, signInMcpServer } from "./oauth.js";
const DEFAULT_TIMEOUT_SECONDS = 60;
const STDERR_TAIL_CHARS = 2_000;
/** Delays between attempts to connect to an HTTP server that failed with a transient error. */
const CONNECT_RETRY_DELAYS_MS = [250, 1_000];
function errorMessage(error) {
    return error instanceof Error ? error.message : String(error);
}
/** Network failures and overloaded or restarting servers, which are worth another attempt. */
function isTransientError(error) {
    if (error instanceof McpHttpError) {
        return error.status === 408 || error.status === 429 || (error.status >= 500 && error.status !== 501);
    }
    return error instanceof TypeError;
}
function signInRequiredMessage(entry) {
    const provider = "url" in entry.config ? entry.config.auth?.provider : undefined;
    return `MCP server "${entry.name}" requires sign-in. Run ${provider ? `/login ${provider}` : "/mcp"} to sign in.`;
}
/** HTTP servers authenticate with OAuth unless the config supplies an `Authorization` header or `auth`. */
function usesOAuth(entry) {
    const { config } = entry;
    if (!("url" in config) || config.auth)
        return false;
    return !Object.keys(config.headers ?? {}).some((header) => header.toLowerCase() === "authorization");
}
/** `~` and `~/…` (also `~\…` on Windows) name the home directory, like in a shell. */
function expandHome(value) {
    if (value === "~")
        return homedir();
    if (value.startsWith("~/") || (process.platform === "win32" && value.startsWith("~\\"))) {
        return join(homedir(), value.slice(2));
    }
    return value;
}
export function createDefaultTransport(entry, cwd, authProvider) {
    const { config, name } = entry;
    if ("url" in config) {
        return new StreamableHttpTransport({
            url: config.url,
            headers: resolveHeadersOrThrow(config.headers, `MCP server "${name}"`),
            authProvider,
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
        stderr: "pipe",
    });
}
/** Servers that do not implement `resources/templates/list` have no templates. */
async function withoutTemplates(list, empty) {
    try {
        return await list();
    }
    catch (error) {
        if (error instanceof McpError && error.code === JSON_RPC_ERROR_CODES.methodNotFound)
            return empty;
        throw error;
    }
}
function listTemplates(client, options = {}) {
    return withoutTemplates(() => client.listResourceTemplates(options), []);
}
/**
 * Resources and templates at connect time, for the counts in `/mcp` and `pi mcp list`. A server
 * whose lists fail still connects: the resource tools list and read its resources on demand.
 */
async function fetchResources(client) {
    const [resources, resourceTemplates] = await Promise.all([
        client.listResources().catch(() => []),
        listTemplates(client).catch(() => []),
    ]);
    return {
        resources: resources.filter((resource) => !isMcpAppResource(resource)),
        resourceTemplates: resourceTemplates.filter((template) => !isMcpAppResource(template)),
    };
}
/** One configured server. Reconnects lazily when a call finds the connection gone. */
export class McpServerConnection {
    entry;
    state = "connecting";
    error;
    tools = [];
    /**
     * Whether the server offers resources. The lists below are what it listed at the last connect or
     * change, without MCP App resources.
     */
    hasResources = false;
    resources = [];
    resourceTemplates = [];
    /** Server instructions from `initialize`, describing its tools as a group. */
    instructions;
    /** Last OAuth challenge from the server; sign-in uses its resource metadata URL and scope. */
    challenge;
    client;
    opening;
    closed = false;
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
        const provider = "url" in this.entry.config ? this.entry.config.auth?.provider : undefined;
        this.authProvider = url
            ? createMcpAuthProvider({
                serverUrl: url,
                store: options.credentials.forServer(url),
                settings: () => this.oauthSettings(),
                onChallenge: (challenge) => {
                    this.challenge = challenge;
                },
            })
            : provider
                ? // Read on every request, so the provider's refreshes apply; MCP stores no copy.
                    { token: async () => options.providerToken?.(provider), settled: async () => { } }
                : undefined;
    }
    get name() {
        return this.entry.name;
    }
    get timeoutMs() {
        return (this.entry.config.timeout ?? DEFAULT_TIMEOUT_SECONDS) * 1000;
    }
    /** Server URL when the server authenticates with OAuth. */
    get oauthUrl() {
        return usesOAuth(this.entry) && "url" in this.entry.config ? this.entry.config.url : undefined;
    }
    oauthSettings() {
        const oauth = "url" in this.entry.config ? this.entry.config.oauth : undefined;
        if (!oauth)
            return {};
        return {
            clientId: oauth.clientId,
            clientSecret: oauth.clientSecret === undefined
                ? undefined
                : resolveConfigValueOrThrow(oauth.clientSecret, `MCP server "${this.entry.name}" oauth.clientSecret`),
            callbackPort: oauth.callbackPort,
            callbackUrl: oauth.callbackUrl,
            scope: oauth.scope,
            clientName: oauth.clientName,
        };
    }
    getClient() {
        if (this.closed)
            return Promise.reject(new Error(`MCP server "${this.entry.name}" is shut down`));
        if (this.client?.connectionState === "connected")
            return Promise.resolve(this.client);
        this.opening ??= this.open().finally(() => {
            this.opening = undefined;
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
        for (let attempt = 1;; attempt++) {
            const client = await this.getClient();
            try {
                return await run(client);
            }
            catch (error) {
                if (readOnly && attempt === 1 && error instanceof McpHttpError && isTransientError(error)) {
                    await new Promise((resolve) => setTimeout(resolve, CONNECT_RETRY_DELAYS_MS[0]));
                    continue;
                }
                if (error instanceof McpSessionExpiredError && attempt === 1) {
                    // The server no longer knows the session (restart, deploy), so it did not run the request.
                    // Retry once on a new session. The old client is detached but not closed: closing would
                    // fail its other in-flight calls, which instead get the same 404 and retry the same way.
                    if (this.client === client)
                        this.client = undefined;
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
        await this.opening?.catch(() => undefined);
        if (this.client)
            await this.dropClient(this.client);
        await this.getClient();
    }
    /** Disconnect after the stored credentials were removed. */
    async signOut() {
        await this.opening?.catch(() => undefined);
        if (this.client)
            await this.dropClient(this.client);
        if (!this.closed)
            this.markNeedsAuth();
    }
    /** OAuth servers that still reject the request after a refresh need the user to sign in again. */
    needsSignIn(error) {
        return (error instanceof McpOAuthAuthorizationRequiredError ||
            (this.authProvider !== undefined && error instanceof McpAuthRequiredError));
    }
    markNeedsAuth() {
        this.state = "needs-auth";
        this.error = undefined;
        this.changed();
    }
    changed() {
        this.onChange?.(this);
    }
    async dropClient(client) {
        if (this.client === client)
            this.client = undefined;
        await client.close().catch(() => undefined);
    }
    async open() {
        this.state = "connecting";
        this.changed();
        const retries = "url" in this.entry.config ? CONNECT_RETRY_DELAYS_MS : [];
        for (let attempt = 0;; attempt++) {
            this.stderrTail = undefined;
            try {
                return await this.connectOnce();
            }
            catch (error) {
                const delay = retries[attempt];
                if (this.closed || delay === undefined || !isTransientError(error)) {
                    throw this.connectFailed(error);
                }
                await new Promise((resolve) => setTimeout(resolve, delay));
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
            roots: [{ uri: pathToFileURL(this.cwd).href, name: basename(this.cwd) }],
        });
        const log = this.log;
        if (log)
            client.onNotification("notifications/message", (params) => log.write(this.entry.name, params));
        let transport;
        try {
            transport = this.createTransport(this.entry, this.cwd, this.authProvider);
            await client.connect(transport);
            client.onNotification("notifications/tools/list_changed", () => {
                void this.refreshTools(client);
            });
            client.onNotification("notifications/resources/list_changed", () => {
                void this.refreshResources(client);
            });
            const stdio = transport instanceof StdioTransport ? transport : undefined;
            client.onClose(() => this.handleClientClose(client, stdio));
            // Servers without the tools capability (prompts or resources only) do not answer tools/list.
            const hasResources = client.serverCapabilities?.resources !== undefined;
            const [tools, resources] = await Promise.all([
                client.serverCapabilities?.tools ? client.listTools() : [],
                hasResources ? fetchResources(client) : { resources: [], resourceTemplates: [] },
            ]);
            if (this.closed)
                throw new Error("shut down while connecting");
            if (client.connectionState !== "connected")
                throw new Error("connection closed during setup");
            this.client = client;
            this.tools = tools;
            this.hasResources = hasResources;
            this.resources = resources.resources;
            this.resourceTemplates = resources.resourceTemplates;
            this.instructions = client.instructions?.trim() || undefined;
            this.state = "connected";
            this.error = undefined;
            this.onTools(this);
            this.changed();
            return client;
        }
        catch (error) {
            await client.close().catch(() => undefined);
            if (transport instanceof StdioTransport) {
                this.stderrTail = transport.stderr.trim().slice(-STDERR_TAIL_CHARS) || undefined;
            }
            throw error;
        }
    }
    connectFailed(error) {
        if (this.needsSignIn(error) && !this.closed) {
            this.markNeedsAuth();
            return new Error(signInRequiredMessage(this.entry));
        }
        this.state = this.closed ? "closed" : "failed";
        this.error = this.stderrTail ? `${errorMessage(error)}\n${this.stderrTail}` : errorMessage(error);
        this.changed();
        return new Error(`MCP server "${this.entry.name}" failed to connect: ${this.error}`);
    }
    /** The transport dropped. The next call reconnects; until then the status shows why. */
    handleClientClose(client, stdio) {
        if (this.client !== client || this.closed)
            return;
        this.client = undefined;
        this.state = "disconnected";
        const stderr = stdio?.stderr.trim().slice(-STDERR_TAIL_CHARS);
        this.error = stderr ? `Connection closed\n${stderr}` : "Connection closed";
        this.changed();
    }
    async refreshTools(client) {
        try {
            const tools = await client.listTools();
            if (this.client !== client || this.closed)
                return;
            this.tools = tools;
            this.onTools(this);
        }
        catch (error) {
            this.error = `Failed to refresh tools: ${errorMessage(error)}`;
        }
        this.changed();
    }
    async refreshResources(client) {
        const { resources, resourceTemplates } = await fetchResources(client);
        if (this.client !== client || this.closed)
            return;
        this.resources = resources;
        this.resourceTemplates = resourceTemplates;
        this.onTools(this);
        this.changed();
    }
    async close() {
        this.closed = true;
        this.state = "closed";
        this.changed();
        const client = this.client;
        this.client = undefined;
        await client?.close().catch(() => undefined);
        // A refresh the server already answered may have rotated the refresh token; exiting before the
        // new tokens are saved would lose the grant.
        await this.authProvider?.settled();
    }
}
//# sourceMappingURL=runtime.js.map