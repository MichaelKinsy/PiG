/**
 * Adapts MCP tools to pi tool definitions.
 *
 * Results map onto pi's model-facing content (text and images). Text over 20KB keeps its start and
 * end with the middle cut out, like Codex does, and the full text is saved to a temp file the model
 * can read. Binary resources other than images are saved to temp files too, and resource links name
 * the `read_mcp_resource` tool. Codemode scripts receive the whole `CallToolResult` without `_meta`
 * (`content` blocks as sent by the server, `structuredContent`, `isError`), never truncated: it is
 * the tool's `structuredContent`, and every MCP tool declares a `CallToolResult` output schema. MCP
 * errors (`isError`) are error results for the model, but scripts still resolve to the result.
 */
import { createMcpResultSchema, limitMcpContent, normalizeMcpInputSchema, toMcpModelContent, } from "../../../pi-mcp/index.js";
import { Container, Spacer, Text } from "../../../../pi-tui.mjs";
import { READ_MCP_RESOURCE_TOOL } from "../../core/mcp-servers.js";
import { formatToolCallWithArgs, getTextOutput, replaceTabs } from "../../core/tools/render-utils.js";
import { keyHint } from "../../modes/interactive/components/keybinding-hints.js";
import { VisualLinePreview } from "../../modes/interactive/components/visual-truncate.js";
import { writeOutputFile } from "../../utils/output-files.js";
/**
 * Tool exposure of an MCP exposure. `codemode` and `deferred` both leave tools out of the codemode
 * description; they differ only in which tool the MCP extension activates to reach them.
 */
export function toToolExposure(exposure) {
    return exposure === "codemode" ? "deferred" : exposure;
}
/** Visual (wrapped) result lines shown before the output is expanded. */
const OUTPUT_PREVIEW_LINES = 5;
/** Tool that reads the resources named by resource links. */
export { READ_MCP_RESOURCE_TOOL };
/** Saves the full text of a truncated result, or a binary resource, to a temp file and returns its path. */
export function saveToTempFile(data, extension) {
    return writeOutputFile("pi-mcp", extension, data);
}
function textOf(content) {
    return content
        .filter((block) => block.type === "text")
        .map((block) => block.text)
        .join("\n");
}
/** Convert an MCP result. `isError` results become error results that keep the structured result. */
export async function convertMcpResult(server, tool, result, options = {}) {
    const saveOutput = options.saveOutput ?? saveToTempFile;
    const converted = await toMcpModelContent(server, result, {
        saveOutput,
        ...(options.readableResources ? { readResourceTool: READ_MCP_RESOURCE_TOOL } : {}),
    });
    if (result.isError && textOf(converted) === "") {
        converted.push({ type: "text", text: `MCP tool ${server}/${tool} returned an error` });
    }
    const { content, fullOutputPath } = await limitMcpContent(converted, saveOutput);
    const { _meta: _ignored, ...scriptResult } = result;
    return {
        content,
        details: { server, tool, ...(fullOutputPath ? { fullOutputPath } : {}) },
        structuredContent: scriptResult,
        ...(result.isError ? { isError: true } : {}),
    };
}
const ANNOTATION_HINTS = ["readOnlyHint", "destructiveHint", "idempotentHint", "openWorldHint"];
/** The boolean hints of an MCP tool's annotations, or undefined when it has none. */
function toToolAnnotations(tool) {
    const annotations = {};
    for (const hint of ANNOTATION_HINTS) {
        const value = tool.annotations?.[hint];
        if (typeof value === "boolean")
            annotations[hint] = value;
    }
    return Object.keys(annotations).length > 0 ? annotations : undefined;
}
export function createMcpToolDefinition(options) {
    const { server, tool } = options;
    const title = tool.title ?? tool.annotations?.title;
    const annotations = toToolAnnotations(tool);
    const label = `${server}/${tool.name}`;
    return {
        name: options.name,
        label,
        description: tool.description?.trim() || title || `MCP tool ${tool.name} from server ${server}`,
        // An object schema, which is what TypeBox schemas are at runtime.
        parameters: normalizeMcpInputSchema(tool.inputSchema),
        // A JSON schema object, which is what TypeBox schemas are at runtime.
        outputSchema: createMcpResultSchema(tool.outputSchema),
        exposure: toToolExposure(options.exposure),
        namespace: options.namespace,
        ...(annotations ? { annotations } : {}),
        ...createMcpToolRenderers(label),
        async execute(_toolCallId, params, signal, onUpdate) {
            const client = await options.getClient();
            const result = await client.callTool(tool.name, (params ?? {}), {
                signal,
                timeoutMs: options.timeoutMs,
                onProgress: (progress) => {
                    const total = progress.total === undefined ? "" : `/${progress.total}`;
                    const text = progress.message ?? `Progress ${progress.progress}${total}`;
                    onUpdate?.({ content: [{ type: "text", text }], details: { server, tool: tool.name } });
                },
            });
            return convertMcpResult(server, tool.name, result, { readableResources: options.readableResources?.() });
        },
    };
}
/** Renderers of calls to an MCP tool, labeled `server/tool`, also used before the tool is registered. */
export function createMcpToolRenderers(label) {
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
            const styled = replaceTabs(output)
                .split("\n")
                .map((line) => theme.fg(color, line))
                .join("\n");
            component.addChild(new Spacer(1));
            if (options.expanded) {
                component.addChild(new Text(styled, 0, 0));
            }
            else {
                // Limit wrapped lines, not logical ones: MCP results are often one long JSON line.
                component.addChild(new VisualLinePreview({
                    text: styled,
                    maxVisualLines: OUTPUT_PREVIEW_LINES,
                    keep: "start",
                    formatHint: (hidden) => `${theme.fg("muted", `... (${hidden} more lines,`)} ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`,
                }));
                const fullOutputPath = result.details?.fullOutputPath;
                if (fullOutputPath)
                    component.addChild(new Text(theme.fg("muted", `Full output: ${fullOutputPath}`), 0, 0));
            }
            return component;
        },
    };
}
//# sourceMappingURL=tools.js.map