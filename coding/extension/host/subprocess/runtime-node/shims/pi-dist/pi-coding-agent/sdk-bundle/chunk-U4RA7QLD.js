import {
  formatSize,
  truncateMiddle
} from "./chunk-YJMZRBMJ.js";
import {
  VisualLinePreview,
  formatToolCallWithArgs,
  getTextOutput,
  replaceTabs
} from "./chunk-65Z52CAH.js";
import {
  keyHint
} from "./chunk-6PEVBP2X.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/extensions/mcp/tools.js
import { createHash, randomBytes } from "node:crypto";
import { writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { toLlmContent } from "../../pi-mcp/index.js";
import { Container, Spacer, Text } from "../../../pi-tui.mjs";
function toToolExposure(exposure) {
  return exposure === "codemode" ? "deferred" : exposure;
}
__name(toToolExposure, "toToolExposure");
var MAX_TOOL_NAME_LENGTH = 64;
var MCP_OUTPUT_MAX_BYTES = 20 * 1024;
var OUTPUT_PREVIEW_LINES = 5;
var READ_MCP_RESOURCE_TOOL = "read_mcp_resource";
async function saveToTempFile(data, extension) {
  const path = join(tmpdir(), `pi-mcp-${randomBytes(8).toString("hex")}${extension}`);
  await writeFile(path, data, { mode: 384 });
  return path;
}
__name(saveToTempFile, "saveToTempFile");
function createMcpToolName(server, tool, isTaken = () => false) {
  const name = `mcp__${server}__${tool}`.replace(/[^A-Za-z0-9_]/g, "_");
  if (name.length <= MAX_TOOL_NAME_LENGTH && !isTaken(name))
    return name;
  const hash = createHash("sha256").update(`${server}\0${tool}`).digest("hex").slice(0, 8);
  return `${name.slice(0, MAX_TOOL_NAME_LENGTH - hash.length - 1)}_${hash}`;
}
__name(createMcpToolName, "createMcpToolName");
function textOf(content) {
  return content.filter((block) => block.type === "text").map((block) => block.text).join("\n");
}
__name(textOf, "textOf");
function createMcpResultSchema(structuredContentSchema) {
  return {
    type: "object",
    properties: {
      content: { type: "array", items: { type: "object" } },
      ...structuredContentSchema ? { structuredContent: structuredContentSchema } : {},
      isError: { type: "boolean" },
      _meta: { type: "object" }
    },
    required: ["content"]
  };
}
__name(createMcpResultSchema, "createMcpResultSchema");
async function limitMcpContent(content, saveOutput = saveToTempFile) {
  const combined = textOf(content);
  const truncation = truncateMiddle(combined, MCP_OUTPUT_MAX_BYTES);
  if (!truncation.truncated)
    return { content };
  let fullOutputPath;
  let where;
  try {
    fullOutputPath = await saveOutput(combined, ".txt");
    where = `[Full output: ${fullOutputPath} (read it with offset/limit)]`;
  } catch (error) {
    where = `[Could not save the full output: ${error instanceof Error ? error.message : String(error)}]`;
  }
  const tokens = Math.ceil(truncation.totalBytes / 4);
  const text = `Warning: truncated output (original token count: ${tokens})
Total output lines: ${truncation.totalLines}

${truncation.content}

${where}`;
  return {
    content: [{ type: "text", text }, ...content.filter((block) => block.type === "image")],
    ...fullOutputPath ? { fullOutputPath } : {}
  };
}
__name(limitMcpContent, "limitMcpContent");
function extensionOf(uri) {
  const path = URL.canParse(uri) ? new URL(uri).pathname : uri;
  return /\.[A-Za-z0-9]{1,8}$/.exec(path)?.[0] ?? ".bin";
}
__name(extensionOf, "extensionOf");
function isTextMimeType(mimeType) {
  if (!mimeType)
    return false;
  const type = mimeType.split(";", 1)[0].trim().toLowerCase();
  return type.startsWith("text/") || type === "application/json" || type.endsWith("+json") || type.endsWith("+xml");
}
__name(isTextMimeType, "isTextMimeType");
async function blockToContent(server, block, options) {
  if (block.type === "resource_link") {
    const details = [block.mimeType, block.size === void 0 ? void 0 : formatSize(block.size)].filter(Boolean);
    const read = options.readableResources ? `. Read it with ${READ_MCP_RESOURCE_TOOL} (server "${server}")` : "";
    const description = block.description ? `: ${block.description}` : "";
    return [
      {
        type: "text",
        text: `[Resource ${block.uri} "${block.title ?? block.name}"${details.length > 0 ? ` (${details.join(", ")})` : ""}${description}${read}]`
      }
    ];
  }
  if (block.type === "resource" && "blob" in block.resource && !block.resource.mimeType?.startsWith("image/")) {
    const { uri, mimeType, blob } = block.resource;
    const data = Buffer.from(blob, "base64");
    if (isTextMimeType(mimeType))
      return [{ type: "text", text: data.toString("utf8") }];
    const kind = `${mimeType ?? "unknown type"}, ${formatSize(data.length)}`;
    try {
      const path = await (options.saveOutput ?? saveToTempFile)(data, extensionOf(uri));
      return [{ type: "text", text: `[Binary resource ${uri} (${kind}) saved to ${path}]` }];
    } catch (error) {
      const reason = error instanceof Error ? error.message : String(error);
      return [{ type: "text", text: `[Binary resource ${uri} (${kind}) could not be saved: ${reason}]` }];
    }
  }
  return toLlmContent({ content: [block] });
}
__name(blockToContent, "blockToContent");
async function toModelContent(server, blocks, options = {}) {
  return (await Promise.all(blocks.map((block) => blockToContent(server, block, options)))).flat();
}
__name(toModelContent, "toModelContent");
async function convertMcpResult(server, tool, result, options = {}) {
  const converted = result.content.length > 0 ? await toModelContent(server, result.content, options) : toLlmContent(result);
  if (result.isError && textOf(converted) === "") {
    converted.push({ type: "text", text: `MCP tool ${server}/${tool} returned an error` });
  }
  const { content, fullOutputPath } = await limitMcpContent(converted, options.saveOutput);
  const { _meta: _ignored, ...scriptResult } = result;
  return {
    content,
    details: { server, tool, ...fullOutputPath ? { fullOutputPath } : {} },
    structuredContent: scriptResult,
    ...result.isError ? { isError: true } : {}
  };
}
__name(convertMcpResult, "convertMcpResult");
function toParameters(schema) {
  return {
    ...schema,
    type: schema.type ?? "object",
    ...schema.properties === void 0 ? { properties: {} } : {}
  };
}
__name(toParameters, "toParameters");
var ANNOTATION_HINTS = ["readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"];
function toToolAnnotations(tool) {
  const annotations = {};
  for (const hint of ANNOTATION_HINTS) {
    const value = tool.annotations?.[hint];
    if (typeof value === "boolean")
      annotations[hint] = value;
  }
  return Object.keys(annotations).length > 0 ? annotations : void 0;
}
__name(toToolAnnotations, "toToolAnnotations");
function createMcpToolDefinition(options) {
  const { server, tool } = options;
  const title = tool.title ?? tool.annotations?.title;
  const annotations = toToolAnnotations(tool);
  const label = `${server}/${tool.name}`;
  return {
    name: options.name,
    label,
    description: tool.description?.trim() || title || `MCP tool ${tool.name} from server ${server}`,
    parameters: toParameters(tool.inputSchema),
    outputSchema: createMcpResultSchema(tool.outputSchema),
    exposure: toToolExposure(options.exposure),
    namespace: options.namespace,
    ...annotations ? { annotations } : {},
    ...createMcpToolRenderers(label),
    async execute(_toolCallId, params, signal, onUpdate) {
      const client = await options.getClient();
      const result = await client.callTool(tool.name, params ?? {}, {
        signal,
        timeoutMs: options.timeoutMs,
        onProgress: /* @__PURE__ */ __name((progress) => {
          const total = progress.total === void 0 ? "" : `/${progress.total}`;
          const text = progress.message ?? `Progress ${progress.progress}${total}`;
          onUpdate?.({ content: [{ type: "text", text }], details: { server, tool: tool.name } });
        }, "onProgress")
      });
      return convertMcpResult(server, tool.name, result, { readableResources: options.readableResources?.() });
    }
  };
}
__name(createMcpToolDefinition, "createMcpToolDefinition");
function createMcpToolRenderers(label) {
  return {
    renderCall(args, theme, context) {
      const component = context.lastComponent ?? new Text("", 0, 0);
      component.setText(formatToolCallWithArgs(label, args, theme, context.expanded));
      return component;
    },
    renderResult(result, options, theme, context) {
      const component = context.lastComponent ?? new Container();
      component.clear();
      const output = getTextOutput(result, context.showImages).trim();
      if (!output)
        return component;
      const color = context.isError ? "error" : "toolOutput";
      const styled = replaceTabs(output).split("\n").map((line) => theme.fg(color, line)).join("\n");
      component.addChild(new Spacer(1));
      if (options.expanded) {
        component.addChild(new Text(styled, 0, 0));
      } else {
        component.addChild(new VisualLinePreview({
          text: styled,
          maxVisualLines: OUTPUT_PREVIEW_LINES,
          keep: "start",
          formatHint: /* @__PURE__ */ __name((hidden) => `${theme.fg("muted", `... (${hidden} more lines,`)} ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`, "formatHint")
        }));
        const fullOutputPath = result.details?.fullOutputPath;
        if (fullOutputPath)
          component.addChild(new Text(theme.fg("muted", `Full output: ${fullOutputPath}`), 0, 0));
      }
      return component;
    }
  };
}
__name(createMcpToolRenderers, "createMcpToolRenderers");

// pi-dist/pi-coding-agent/extensions/mcp/resources.js
var LIST_MCP_RESOURCES_TOOL = "list_mcp_resources";
var LIST_MCP_RESOURCE_TEMPLATES_TOOL = "list_mcp_resource_templates";
function isMcpAppResource(item) {
  const uri = item.uri ?? item.uriTemplate ?? "";
  return uri.startsWith("ui://") || /;\s*profile\s*=\s*"?mcp-app"?/i.test(item.mimeType ?? "");
}
__name(isMcpAppResource, "isMcpAppResource");
function listed(server, item) {
  const { _meta, icons: _icons, ...rest } = item;
  return { server, ...rest };
}
__name(listed, "listed");
var stringProperty = /* @__PURE__ */ __name((description) => ({ type: "string", description }), "stringProperty");
var SERVER_FILTER = stringProperty("MCP server name. Omit to list every server with resources.");
var CURSOR = stringProperty("Opaque cursor from a previous call with the same server; omit for the first page.");
var LIST_PARAMETERS = {
  type: "object",
  properties: { server: SERVER_FILTER, cursor: CURSOR },
  additionalProperties: false
};
var READ_PARAMETERS = {
  type: "object",
  properties: {
    server: stringProperty("MCP server name exactly as configured. Must match the 'server' field returned by list_mcp_resources."),
    uri: stringProperty("Resource URI to read. Must be one of the URIs returned by list_mcp_resources.")
  },
  required: ["server", "uri"],
  additionalProperties: false
};
var optionalString = { type: "string" };
var LISTING_ERRORS = {
  type: "array",
  description: "Servers that could not be listed",
  items: {
    type: "object",
    properties: { server: { type: "string" }, error: { type: "string" } },
    required: ["server", "error"]
  }
};
var LIST_OUTPUT_SCHEMA = {
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
          size: { type: "number" }
        },
        required: ["server", "uri", "name"]
      }
    },
    nextCursor: optionalString,
    errors: LISTING_ERRORS
  },
  required: ["resources"]
};
var LIST_TEMPLATES_OUTPUT_SCHEMA = {
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
          mimeType: optionalString
        },
        required: ["server", "uriTemplate", "name"]
      }
    },
    nextCursor: optionalString,
    errors: LISTING_ERRORS
  },
  required: ["resourceTemplates"]
};
var READ_OUTPUT_SCHEMA = {
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
            required: ["uri", "text"]
          },
          {
            type: "object",
            properties: {
              uri: { type: "string" },
              mimeType: optionalString,
              blob: { type: "string", description: "base64" }
            },
            required: ["uri", "blob"]
          }
        ]
      }
    }
  },
  required: ["server", "uri", "contents"]
};
function stringArgument(params, key) {
  const value = params?.[key];
  if (value === void 0 || value === null)
    return void 0;
  if (typeof value !== "string")
    throw new Error(`${key} must be a string`);
  return value.trim() || void 0;
}
__name(stringArgument, "stringArgument");
function errorMessage(error) {
  return error instanceof Error ? error.message : String(error);
}
__name(errorMessage, "errorMessage");
async function jsonResult(tool, server, payload) {
  const { content, fullOutputPath } = await limitMcpContent([{ type: "text", text: JSON.stringify(payload) }]);
  return {
    content,
    details: { server: server ?? "", tool, ...fullOutputPath ? { fullOutputPath } : {} },
    structuredContent: payload
  };
}
__name(jsonResult, "jsonResult");
function createMcpResourceToolDefinitions(options) {
  const readOnly = { readOnlyHint: true };
  const findServer = /* @__PURE__ */ __name((name) => {
    const servers = options.servers();
    const server = servers.find((candidate) => candidate.name === name);
    if (server)
      return server;
    const available = servers.map((candidate) => candidate.name).join(", ");
    throw new Error(`MCP server "${name}" has no resources${available ? `. Servers with resources: ${available}` : ""}`);
  }, "findServer");
  const list = /* @__PURE__ */ __name(async (params, signal, key, page, all) => {
    const serverName = stringArgument(params, "server");
    const cursor = stringArgument(params, "cursor");
    const visible = /* @__PURE__ */ __name((item) => !isMcpAppResource(item), "visible");
    if (serverName) {
      const server = findServer(serverName);
      const result = await page(server, cursor, { signal, timeoutMs: server.timeoutMs });
      return {
        server: server.name,
        [key]: result.items.filter(visible).map((item) => listed(server.name, item)),
        ...result.nextCursor === void 0 ? {} : { nextCursor: result.nextCursor }
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
    return { [key]: items, ...errors.length > 0 ? { errors } : {} };
  }, "list");
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
    }
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
    }
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
      const blocks = result.contents.flatMap((contents2) => [
        ...result.contents.length > 1 ? [{ type: "text", text: `${contents2.uri}:` }] : [],
        { type: "resource", resource: contents2 }
      ]);
      const converted = await toModelContent(server.name, blocks);
      const { content, fullOutputPath } = await limitMcpContent(converted.length > 0 ? converted : [{ type: "text", text: `Resource ${uri} is empty.` }]);
      const contents = result.contents.map(({ _meta: _ignored, ...rest }) => rest);
      return {
        content,
        details: {
          server: server.name,
          tool: READ_MCP_RESOURCE_TOOL,
          ...fullOutputPath ? { fullOutputPath } : {}
        },
        structuredContent: { server: server.name, uri, contents }
      };
    }
  };
  return [listResources, listTemplates, readResource];
}
__name(createMcpResourceToolDefinitions, "createMcpResourceToolDefinitions");

export {
  READ_MCP_RESOURCE_TOOL,
  createMcpToolName,
  createMcpToolDefinition,
  createMcpToolRenderers,
  LIST_MCP_RESOURCES_TOOL,
  LIST_MCP_RESOURCE_TEMPLATES_TOOL,
  isMcpAppResource,
  createMcpResourceToolDefinitions
};
