/**
 * The `codemode` tool: the model writes JavaScript that calls other tools. Scripts use `tools`,
 * `ALL_TOOLS`, `text()`, `image()`, `exit()`, `store()`/`load()`, `console.*`, and `return <value>`,
 * may start with a `// @options:` line, and reach the model catalog, classifiers, and image models
 * through `models.*`. Results start with a "Script completed" or "Script failed" header.
 *
 * Scripts can call the agent loop's nested tools: active `direct` tools and every `codemode` or
 * `deferred` tool. Nested calls run through the agent loop's tool pipeline (`ctx.executeTool`), so
 * validation, `tool_call`/`tool_result` hooks, and permission checks apply exactly as for direct
 * calls. Only the script's output reaches the model; nested results do not.
 *
 * Nested results are handed to the script as follows:
 * - A tool that declares `outputSchema` resolves to its `structuredContent`, also for error
 *   results that carry one (MCP tools resolve to their `CallToolResult`, including `isError`).
 * - Any other tool resolves to its text content as one string.
 * - A failed, blocked, or invalid call rejects with an Error carrying the tool's error text.
 *
 * A script that fails returns a normal error result that keeps its partial output, followed by
 * "Script error:" and the error. `store(key, value)` and `load(key)` keep JSON values across
 * calls; successful scripts append their writes to the session as `codemode-store` custom entries,
 * so each branch sees the values written on its own path.
 */
import { join } from "node:path";
import { MCP_TYPESCRIPT_PREAMBLE, mcpStructuredContentSchema, renderToolOutputType, renderToolSample, toCodemodeIdentifier, } from "../../../pi-codemode/declarations.js";
import { CODEMODE_SOURCE_GRAMMAR } from "../../../pi-codemode/source.js";
import { Type } from "../../../../typebox.mjs";
import { getDocsPath } from "../../config.js";
import { wrapToolDefinition } from "../../core/tools/tool-definition-wrapper.js";
import { loadCodemodeExecutor } from "./execute.lazy.js";
import { codemodeRenderers } from "./renderer.js";
export const CODEMODE_TOOL_NAME = "codemode";
/** Custom entry type holding one script's `store()` writes: {@link CodemodeStoreEntryData}. */
export const CODEMODE_STORE_ENTRY_TYPE = "codemode-store";
const TEXT_OUTPUT_SCHEMA = { type: "string" };
export const codemodeSchema = Type.Object({
    code: Type.String({
        description: "Raw JavaScript source.",
    }),
});
/**
 * Whether a registered tool is this package's `codemode` tool rather than another extension's tool
 * with the same name. Compares the parameter schema, which the definition passes through by reference.
 */
export function isCodemodeTool(tool) {
    return tool.name === CODEMODE_TOOL_NAME && tool.parameters === codemodeSchema;
}
export const codemodeToolSystemPromptContribution = {
    snippet: "Run JavaScript that calls other tools",
    guidelines: [
        "Use codemode to batch independent tool calls (Promise.allSettled), chain them, or filter large output, instead of many separate calls.",
    ],
};
/** The reference for scripts: globals, tool results, `store()`, the `models` API, and limits. */
export const CODEMODE_DOCS_PATH = join(getDocsPath(), "codemode.md");
const DESCRIPTION_INTRO = `Run JavaScript that calls other tools. The input is raw JavaScript (not JSON, no code fence), run as an async function body in a QuickJS sandbox: top-level \`await\` and \`return\` work. No Node, file system, network, or timers.
- \`await tools.<name>({ ...args })\` resolves to a string, or an object if the tool's declaration says so, and rejects with an Error on failure. Calls still running when the script ends are cancelled.
- Optional first line: \`// @options: {"max_output_tokens": 10000, "timeout_ms": 60000}\``;
/** One line per global. The details live in {@link CODEMODE_DOCS_PATH}. */
function describeGlobals(models) {
    const lines = [
        "Globals:",
        "- `text(value)`, `image(dataUrlOrImageBlock)`, `console.log(...)`, and top-level `return` add output; `exit()` ends the script. `image()` also saves the image to a temp file and the result names its path.",
        "- `store(key, value)` and `load(key)` keep JSON values across codemode calls.",
        "- `ALL_TOOLS`, `searchTools(query, { limit?, namespace? })`, `describeTool(name)`, `describeNamespace(name)`: find unlisted tools, such as MCP tools.",
    ];
    if (models) {
        lines.push(`- \`models\`: classifiers and image generation. Read ${CODEMODE_DOCS_PATH} first.`);
    }
    return lines.join("\n");
}
/** Default for {@link CodemodeDescriptionOptions.inlineBudget}, in estimated tokens. */
export const DEFAULT_CODEMODE_INLINE_BUDGET = 3000;
/** Characters per token when estimating the cost of a tool section. */
const CHARS_PER_TOKEN = 4;
/** What a script sees of a tool. Tools without an output schema resolve to their text output. */
export function toCodemodeDeclaration(tool) {
    return {
        name: tool.name,
        description: tool.description,
        inputSchema: tool.parameters,
        outputSchema: tool.outputSchema ?? TEXT_OUTPUT_SCHEMA,
    };
}
/** Tools a script may call: every given tool except the codemode tool itself. */
export function getCodemodeCallableTools(tools) {
    return tools.filter((tool) => tool.name !== CODEMODE_TOOL_NAME);
}
/** `### \`id\` (\`raw name\`)` followed by the tool's description and declaration. */
function renderToolSection(declaration) {
    const id = toCodemodeIdentifier(declaration.name);
    const heading = id === declaration.name ? `### \`${id}\`` : `### \`${id}\` (\`${declaration.name}\`)`;
    return `${heading}\n${renderToolSample(declaration).trim()}`;
}
/**
 * Pick the tool sections that fit the budget, like OpenCode's catalog: in each round every group
 * (tools without a namespace first, then namespaces by name) places its cheapest remaining tool; a
 * group whose next tool does not fit drops out while the others continue. Every namespace is
 * represented before any namespace is complete.
 */
function selectCatalog(groups, budget) {
    if (budget === undefined)
        return new Set(groups.flatMap((group) => group.entries.map((entry) => entry.name)));
    const queues = groups.map((group) => [...group.entries].sort((a, b) => a.cost - b.cost));
    const shown = new Set();
    let remaining = budget;
    let active = queues.filter((queue) => queue.length > 0);
    while (active.length > 0) {
        active = active.filter((queue) => {
            const next = queue[0];
            if (next.cost > remaining)
                return false;
            remaining -= next.cost;
            shown.add(next.name);
            queue.shift();
            return queue.length > 0;
        });
    }
    return shown;
}
/**
 * Model-facing description: the helper list, guidance for finding tools that are not listed, the
 * shared MCP types when listed tools need them, the `models` API, and one section per listed tool,
 * grouped by namespace. Deferred tools are never listed and do not affect the description at all, so
 * it stays the same while MCP servers connect or change their tools. Tool sections are limited to
 * `inlineBudget`.
 */
export function createCodemodeDescription(tools, options = {}) {
    const declarations = getCodemodeCallableTools(tools)
        .filter((tool) => !options.deferred?.has(tool.name))
        .map(toCodemodeDeclaration);
    const groups = new Map([["", { namespace: undefined, entries: [] }]]);
    for (const declaration of declarations) {
        const namespace = options.namespaces?.get(declaration.name);
        const key = namespace ? `ns:${namespace.name}` : "";
        const group = groups.get(key) ?? { namespace, entries: [] };
        groups.set(key, group);
        const section = renderToolSection(declaration);
        group.entries.push({ name: declaration.name, section, cost: Math.ceil(section.length / CHARS_PER_TOKEN) });
    }
    const ordered = [...groups.values()].sort((a, b) => a.namespace === undefined ? -1 : b.namespace === undefined ? 1 : a.namespace.name.localeCompare(b.namespace.name));
    const shown = selectCatalog(ordered, options.inlineBudget);
    const sections = [DESCRIPTION_INTRO, describeGlobals(options.models === true)];
    if (declarations.some((declaration) => shown.has(declaration.name) && mcpStructuredContentSchema(declaration.outputSchema) !== undefined)) {
        sections.push(`Shared MCP Types:\n\`\`\`ts\n${MCP_TYPESCRIPT_PREAMBLE}\n\`\`\``);
    }
    if (declarations.length === 0)
        return sections.join("\n\n");
    const toolSections = ["Nested tools:"];
    for (const { namespace, entries } of ordered) {
        const visible = entries.filter((entry) => shown.has(entry.name));
        if (namespace) {
            // Only tools that did not fit the budget are counted as not listed here.
            const listing = visible.length === entries.length
                ? ""
                : visible.length === 0
                    ? " (tools not listed)"
                    : " (some tools not listed)";
            const description = namespace.description?.trim();
            toolSections.push(`## ${namespace.name}${listing}${description ? `\n${description}` : ""}`);
        }
        for (const entry of visible)
            toolSections.push(entry.section);
    }
    sections.push(toolSections.join("\n\n"));
    return sections.join("\n\n");
}
/**
 * What a script call resolves to, in one line: `a string`, the field names of an object
 * (`{ output, exit_code, full_output_path? }`), or the rendered type for anything else.
 */
function describeOutput(schema) {
    const type = renderToolOutputType(schema);
    if (type === "string")
        return "a string";
    const object = typeof schema === "object" ? schema : undefined;
    const properties = object?.properties;
    if (object?.type === "object" &&
        typeof properties === "object" &&
        properties !== null &&
        mcpStructuredContentSchema(schema) === undefined) {
        const required = new Set(Array.isArray(object.required) ? object.required : []);
        const fields = Object.keys(properties).map((name) => (required.has(name) ? name : `${name}?`));
        return `\`{ ${fields.join(", ")} }\``;
    }
    return `\`${type.replace(/\s+/g, " ")}\``;
}
/**
 * A declared tool's description followed by how scripts call it and what the call resolves to. The
 * arguments are the tool's declared parameters, so they are not repeated.
 */
function describeScriptCall(tool) {
    return `${tool.description.trim()}\n\nCodemode: \`tools.${toCodemodeIdentifier(tool.name)}(args)\` resolves to ${describeOutput(toCodemodeDeclaration(tool).outputSchema)}.`;
}
/**
 * How the codemode tool presents tools that are both declared and callable from scripts:
 * - `on`: their descriptions say how scripts call them, and the codemode description
 *   lists only the callable tools without `direct` exposure.
 * - `only`: the codemode description lists every callable tool, and requests leave out the
 *   declarations of active `direct` tools.
 *
 * Listing by exposure, not by the active set, keeps the codemode description unchanged when
 * `tool_search` loads a tool, so loads do not redeclare codemode.
 */
function prepareCodemodeLoadout(loadout, options) {
    const mode = options.getMode?.() ?? "on";
    const isDirect = (tool) => loadout.getExposure(tool.name) === "direct";
    const callable = getCodemodeCallableTools(loadout.callable);
    const callableNames = new Set(callable.map((tool) => tool.name));
    const descriptions = {};
    if (mode === "on") {
        for (const tool of loadout.declared) {
            if (callableNames.has(tool.name))
                descriptions[tool.name] = describeScriptCall(tool);
        }
    }
    const listed = mode === "only" ? callable : callable.filter((tool) => !isDirect(tool));
    const namespaces = new Map(listed.flatMap((tool) => {
        const namespace = loadout.getNamespace(tool.name);
        return namespace ? [[tool.name, namespace]] : [];
    }));
    descriptions[CODEMODE_TOOL_NAME] = createCodemodeDescription(listed, {
        models: options.models === true,
        namespaces,
        deferred: new Set(listed.filter((tool) => loadout.getExposure(tool.name) === "deferred").map((tool) => tool.name)),
        inlineBudget: options.getInlineBudget?.() ?? DEFAULT_CODEMODE_INLINE_BUDGET,
    });
    const declaredNames = new Set(loadout.declared.map((tool) => tool.name));
    return {
        descriptions,
        hiddenDeclarations: mode === "only"
            ? callable.filter((tool) => isDirect(tool) && declaredNames.has(tool.name)).map((tool) => tool.name)
            : [],
    };
}
export function createCodemodeToolDefinition(options = {}) {
    return {
        name: CODEMODE_TOOL_NAME,
        label: CODEMODE_TOOL_NAME,
        // Replaced with the declarations of the callable tools when the tool is activated.
        description: createCodemodeDescription([], { models: options.models === true }),
        promptSnippet: codemodeToolSystemPromptContribution.snippet,
        promptGuidelines: [...codemodeToolSystemPromptContribution.guidelines],
        parameters: codemodeSchema,
        // Scripts must not start other scripts.
        exposure: "model-only",
        prepareLoadout: (loadout) => prepareCodemodeLoadout(loadout, options),
        // Capable models write the script as raw text instead of a JSON-escaped string.
        constrainedSampling: { type: "grammar", variants: { openai_lark: CODEMODE_SOURCE_GRAMMAR } },
        // The sandbox (worker, QuickJS wasm) loads on the first call, not at startup.
        execute: async (toolCallId, params, signal, onUpdate, ctx) => (await loadCodemodeExecutor()).executeCodemode(toolCallId, params, signal, onUpdate, ctx, options),
        ...codemodeRenderers,
    };
}
/**
 * Create the codemode tool as an AgentTool. The description lists the given tools; the script can
 * call whatever tools the agent loop provides at execution time.
 */
export function createCodemodeTool(tools = [], options = {}) {
    const definition = createCodemodeToolDefinition(options);
    const tool = wrapToolDefinition(definition);
    Object.assign(tool, {
        description: createCodemodeDescription(tools, { models: options.models === true }),
        promptSnippet: definition.promptSnippet,
        promptGuidelines: definition.promptGuidelines,
    });
    return tool;
}
//# sourceMappingURL=tool.js.map