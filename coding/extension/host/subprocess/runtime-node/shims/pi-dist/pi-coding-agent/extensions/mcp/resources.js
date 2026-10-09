/**
 * MCP resources, through the tools Codex and opencode use: `list_mcp_resources`,
 * `list_mcp_resource_templates`, and `read_mcp_resource`. They take a `server` argument and cover
 * every connected server with resources, so models trained on those tools use them unchanged.
 *
 * Listings are JSON, as in Codex: `{ server?, resources: [{ server, ...resource }], nextCursor? }`.
 * With a `server`, one page is listed and `cursor` continues it; without, every page of every server.
 * MCP App resources (`ui://` URIs and `profile=mcp-app` HTML) are left out, since they are user
 * interfaces for hosts that render them, and so are icons. Read resources become text and images for
 * the model; binary resources are saved to temp files. Scripts get the JSON payloads.
 */
import { LIST_MCP_RESOURCE_TEMPLATES_TOOL, LIST_MCP_RESOURCES_TOOL, READ_MCP_RESOURCE_TOOL, } from "../../core/mcp-servers.js";
import { limitMcpContent, toModelContent, toToolExposure } from "./tools.js";
export { LIST_MCP_RESOURCE_TEMPLATES_TOOL, LIST_MCP_RESOURCES_TOOL, READ_MCP_RESOURCE_TOOL };
/** MCP App user interfaces, which only hosts that render them can use. */
export function isMcpAppResource(item) {
    const uri = item.uri ?? item.uriTemplate ?? "";
    return uri.startsWith("ui://") || /;\s*profile\s*=\s*"?mcp-app"?/i.test(item.mimeType ?? "");
}
/** A listed resource or template without `_meta` and icons, tagged with its server. */
function listed(server, item) {
    const { _meta, icons: _icons, ...rest } = item;
    return { server, ...rest };
}
const stringProperty = (description) => ({ type: "string", description });
const SERVER_FILTER = stringProperty("MCP server name. Omit to list every server with resources.");
const CURSOR = stringProperty("Opaque cursor from a previous call with the same server; omit for the first page.");
const LIST_PARAMETERS = {
    type: "object",
    properties: { server: SERVER_FILTER, cursor: CURSOR },
    additionalProperties: false,
};
const READ_PARAMETERS = {
    type: "object",
    properties: {
        server: stringProperty("MCP server name exactly as configured. Must match the 'server' field returned by list_mcp_resources."),
        uri: stringProperty("Resource URI to read. Must be one of the URIs returned by list_mcp_resources."),
    },
    required: ["server", "uri"],
    additionalProperties: false,
};
const optionalString = { type: "string" };
const LISTING_ERRORS = {
    type: "array",
    description: "Servers that could not be listed",
    items: {
        type: "object",
        properties: { server: { type: "string" }, error: { type: "string" } },
        required: ["server", "error"],
    },
};
const LIST_OUTPUT_SCHEMA = {
    type: "object",
    properties: {
        server: optionalString,
        resources: {
            type: "array",
            items: {
                type: "object",
                properties: {
                    server: { type: "string" },
                    uri: { type: "string" },
                    name: { type: "string" },
                    title: optionalString,
                    description: optionalString,
                    mimeType: optionalString,
                    size: { type: "number" },
                },
                required: ["server", "uri", "name"],
            },
        },
        nextCursor: optionalString,
        errors: LISTING_ERRORS,
    },
    required: ["resources"],
};
const LIST_TEMPLATES_OUTPUT_SCHEMA = {
    type: "object",
    properties: {
        server: optionalString,
        resourceTemplates: {
            type: "array",
            items: {
                type: "object",
                properties: {
                    server: { type: "string" },
                    uriTemplate: { type: "string", description: "RFC 6570 URI template" },
                    name: { type: "string" },
                    title: optionalString,
                    description: optionalString,
                    mimeType: optionalString,
                },
                required: ["server", "uriTemplate", "name"],
            },
        },
        nextCursor: optionalString,
        errors: LISTING_ERRORS,
    },
    required: ["resourceTemplates"],
};
const READ_OUTPUT_SCHEMA = {
    type: "object",
    properties: {
        server: { type: "string" },
        uri: { type: "string" },
        contents: {
            type: "array",
            items: {
                anyOf: [
                    {
                        type: "object",
                        properties: { uri: { type: "string" }, mimeType: optionalString, text: { type: "string" } },
                        required: ["uri", "text"],
                    },
                    {
                        type: "object",
                        properties: {
                            uri: { type: "string" },
                            mimeType: optionalString,
                            blob: { type: "string", description: "base64" },
                        },
                        required: ["uri", "blob"],
                    },
                ],
            },
        },
    },
    required: ["server", "uri", "contents"],
};
function stringArgument(params, key) {
    const value = params?.[key];
    if (value === undefined || value === null)
        return undefined;
    if (typeof value !== "string")
        throw new Error(`${key} must be a string`);
    return value.trim() || undefined;
}
function errorMessage(error) {
    return error instanceof Error ? error.message : String(error);
}
async function jsonResult(tool, server, payload) {
    const { content, fullOutputPath } = await limitMcpContent([{ type: "text", text: JSON.stringify(payload) }]);
    return {
        content,
        details: { server: server ?? "", tool, ...(fullOutputPath ? { fullOutputPath } : {}) },
        structuredContent: payload,
    };
}
/**
 * The three resource tools. `servers` returns the servers whose resources they reach, at call time.
 */
export function createMcpResourceToolDefinitions(options) {
    const readOnly = { readOnlyHint: true };
    const findServer = (name) => {
        const servers = options.servers();
        const server = servers.find((candidate) => candidate.name === name);
        if (server)
            return server;
        const available = servers.map((candidate) => candidate.name).join(", ");
        throw new Error(`MCP server "${name}" has no resources${available ? `. Servers with resources: ${available}` : ""}`);
    };
    /** One page of one server, or every page of every server. */
    const list = async (params, signal, key, page, all) => {
        const serverName = stringArgument(params, "server");
        const cursor = stringArgument(params, "cursor");
        const visible = (item) => !isMcpAppResource(item);
        if (serverName) {
            const server = findServer(serverName);
            const result = await page(server, cursor, { signal, timeoutMs: server.timeoutMs });
            return {
                server: server.name,
                [key]: result.items.filter(visible).map((item) => listed(server.name, item)),
                ...(result.nextCursor === undefined ? {} : { nextCursor: result.nextCursor }),
            };
        }
        if (cursor)
            throw new Error("cursor can only be used when a server is specified");
        const servers = [...options.servers()].sort((a, b) => a.name.localeCompare(b.name));
        const results = await Promise.allSettled(servers.map((server) => all(server, { signal, timeoutMs: server.timeoutMs })));
        const items = [];
        const errors = [];
        results.forEach((result, index) => {
            const server = servers[index].name;
            if (result.status === "fulfilled")
                items.push(...result.value.filter(visible).map((item) => listed(server, item)));
            else
                errors.push({ server, error: errorMessage(result.reason) });
        });
        return { [key]: items, ...(errors.length > 0 ? { errors } : {}) };
    };
    const listResources = {
        name: LIST_MCP_RESOURCES_TOOL,
        label: LIST_MCP_RESOURCES_TOOL,
        description: "Lists resources provided by MCP servers. Resources allow servers to share data that provides context to language models, such as files, database schemas, or application-specific information. Prefer resources over web search when possible.",
        parameters: LIST_PARAMETERS,
        outputSchema: LIST_OUTPUT_SCHEMA,
        exposure: toToolExposure(options.exposure),
        annotations: readOnly,
        async execute(_toolCallId, params, signal) {
            const payload = await list(params, signal, "resources", async (server, cursor, requestOptions) => {
                const result = await server.resourcesPage(cursor, requestOptions);
                return { items: result.resources, nextCursor: result.nextCursor };
            }, (server, requestOptions) => server.allResources(requestOptions));
            return jsonResult(LIST_MCP_RESOURCES_TOOL, stringArgument(params, "server"), payload);
        },
    };
    const listTemplates = {
        name: LIST_MCP_RESOURCE_TEMPLATES_TOOL,
        label: LIST_MCP_RESOURCE_TEMPLATES_TOOL,
        description: "Lists resource templates provided by MCP servers. Parameterized resource templates allow servers to share data that takes parameters and provides context to language models, such as files, database schemas, or application-specific information. Prefer resource templates over web search when possible.",
        parameters: LIST_PARAMETERS,
        outputSchema: LIST_TEMPLATES_OUTPUT_SCHEMA,
        exposure: toToolExposure(options.exposure),
        annotations: readOnly,
        async execute(_toolCallId, params, signal) {
            const payload = await list(params, signal, "resourceTemplates", async (server, cursor, requestOptions) => {
                const result = await server.resourceTemplatesPage(cursor, requestOptions);
                return { items: result.resourceTemplates, nextCursor: result.nextCursor };
            }, (server, requestOptions) => server.allResourceTemplates(requestOptions));
            return jsonResult(LIST_MCP_RESOURCE_TEMPLATES_TOOL, stringArgument(params, "server"), payload);
        },
    };
    const readResource = {
        name: READ_MCP_RESOURCE_TOOL,
        label: READ_MCP_RESOURCE_TOOL,
        description: "Read a specific resource from an MCP server given the server name and resource URI.",
        parameters: READ_PARAMETERS,
        outputSchema: READ_OUTPUT_SCHEMA,
        exposure: toToolExposure(options.exposure),
        annotations: readOnly,
        async execute(_toolCallId, params, signal) {
            const serverName = stringArgument(params, "server");
            const uri = stringArgument(params, "uri");
            if (!serverName)
                throw new Error("server must be provided");
            if (!uri)
                throw new Error("uri must be provided");
            const server = findServer(serverName);
            const result = await server.readResource(uri, { signal, timeoutMs: server.timeoutMs });
            // Several contents (for example a directory) are labeled with their URIs.
            const blocks = result.contents.flatMap((contents) => [
                ...(result.contents.length > 1 ? [{ type: "text", text: `${contents.uri}:` }] : []),
                { type: "resource", resource: contents },
            ]);
            const converted = await toModelContent(server.name, blocks);
            const { content, fullOutputPath } = await limitMcpContent(converted.length > 0 ? converted : [{ type: "text", text: `Resource ${uri} is empty.` }]);
            const contents = result.contents.map(({ _meta: _ignored, ...rest }) => rest);
            return {
                content,
                details: {
                    server: server.name,
                    tool: READ_MCP_RESOURCE_TOOL,
                    ...(fullOutputPath ? { fullOutputPath } : {}),
                },
                structuredContent: { server: server.name, uri, contents },
            };
        },
    };
    return [listResources, listTemplates, readResource];
}
//# sourceMappingURL=resources.js.map