/**
 * The `codemode` tool: the model writes JavaScript that calls other tools. Scripts use `tools`,
 * `ALL_TOOLS`, `text()`, `image()`, `exit()`, `store()`/`load()`, `console.*`, and `return <value>`,
 * may start with a `// @options:` line, and reach the model catalog and classifiers through
 * `models.*`. Results start with a "Script completed" or "Script failed" header.
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
import { MCP_TYPESCRIPT_PREAMBLE, mcpStructuredContentSchema, renderDeclarations, renderToolSample, toCodemodeIdentifier, } from "../../../pi-codemode/declarations.js";
import { CODEMODE_SOURCE_GRAMMAR } from "../../../pi-codemode/source.js";
import { Type } from "../../../../typebox.mjs";
import { wrapToolDefinition } from "../../core/tools/tool-definition-wrapper.js";
import { loadCodemodeExecutor } from "./execute.lazy.js";
import { codemodeRenderers } from "./renderer.js";
export const CODEMODE_TOOL_NAME = "codemode";
/** Custom entry type holding one script's `store()` writes: {@link CodemodeStoreEntryData}. */
export const CODEMODE_STORE_ENTRY_TYPE = "codemode-store";
const TEXT_OUTPUT_SCHEMA = { type: "string" };
export const codemodeSchema = Type.Object({
    code: Type.String({
        description: 'Raw JavaScript source. Top-level await and return work. May start with a `// @options: {"max_output_tokens": 1000}` line.',
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
    snippet: "Run JavaScript that calls other tools (chains, loops, Promise.all, filtering large results)",
    guidelines: [
        "Use codemode to batch or chain several tool calls, or to filter large tool output down to what you need, instead of issuing many individual tool calls. Batch independent calls in one codemode call using await Promise.allSettled([...]).",
    ],
};
const DESCRIPTION_INTRO = `Run JavaScript code to orchestrate/compose tool calls
- Evaluates the provided JavaScript code in a fresh QuickJS sandbox as the body of an async function: top-level \`await\` and \`return\` work.
- All nested tools are available on the global \`tools\` object, for example \`await tools.read(...)\`. Tool names are exposed as normalized JavaScript identifiers, for example \`await tools.mcp__ologs__get_profile(...)\`.
- Nested tool methods take an object as their input argument.
- Nested tools return either an object or a string, based on the description.
- A nested tool call that fails, is blocked, or gets invalid arguments rejects with an Error carrying the tool's error text.
- Runs raw JavaScript -- no Node, no file system, no network access, no timers.
- Accepts raw JavaScript source text, not JSON, quoted strings, or markdown code fences.
- You may optionally start the tool input with a first line like \`// @options: {"max_output_tokens": 1000, "timeout_ms": 60000}\`.
- \`max_output_tokens\` sets the token budget for the script's output. Defaults to 10000 tokens.
- \`timeout_ms\` sets a hard deadline for the whole script. By default there is none.
- When the JS code is fully evaluated, calls that are still running are cancelled and unawaited promises are silently discarded.
- Tool calls are real and have side effects. If the script fails partway, earlier calls are not undone.
- Scripts have a 256 MB memory limit; exceeding it throws \`InternalError: out of memory\`. Filter or aggregate large data instead of accumulating it.

- Global helpers:
- \`exit()\`: Immediately ends the current script successfully (like an early return from the top level).
- \`text(value: string | number | boolean | undefined | null)\`: Appends a text item. Non-string values are stringified with \`JSON.stringify(...)\` when possible.
- \`image(imageUrlOrItem: string | { image_url: string } | ImageContent)\`: Appends an image item. \`image_url\` should be a base64-encoded \`data:\` URL. To forward an MCP tool image, pass an individual \`ImageContent\` block from \`result.content\`, for example \`image(result.content[0])\`.
- \`store(key: string, value: any)\`: stores a serializable value under a string key for later \`codemode\` calls in the same session. Storing \`undefined\` deletes the key. Writes are kept only if the script succeeds.
- \`load(key: string)\`: returns the stored value for a string key, or \`undefined\` if it is missing.
- \`ALL_TOOLS\`: metadata for the enabled nested tools as \`{ name, description }\` entries.
- \`searchTools(query: string, options?: { limit?: number; namespace?: string })\`: resolves to the nested tools that best match the query (BM25, default limit 8), as \`{ name, description }\` entries like \`ALL_TOOLS\`.
- \`describeTool(name: string)\`: resolves to the description and declaration of a nested tool, or \`undefined\`.
- \`describeNamespace(name: string)\`: resolves to \`{ name, description?, instructions?, tools }\` for a namespace of nested tools, such as an MCP server: its usage instructions and the names of its tools, or \`undefined\`.
- \`console.log(...)\` and the other \`console\` methods append a text item like \`text()\`.
- \`return value\` at the top level appends the value like \`text()\`.`;
const MODEL_TYPES = `type ModelType = "chat" | "image" | "classifier";
/** A model catalog entry. \`provider\` and \`id\` identify it; the other fields depend on the type. */
interface ModelInfo {
  type?: ModelType;
  provider: string;
  id: string;
  name: string;
  api: string;
  input: ("text" | "image")[];
  contextWindow?: number;
  [key: string]: unknown;
}
type ClassifierQuestion =
  | { type: "choice"; instructions: string; criteria: Record<string, string> }
  | { type: "score"; instructions: string; criteria: string[] }
  | { type: "bool"; instructions: string; criteria: { true: string; false: string } };
type ClassifierAnswer =
  | { type: "choice"; choice: string; probabilities: Record<string, number>; confidence: number }
  | { type: "score"; score: number; confidence: number }
  | { type: "bool"; probability: number };
interface ClassifierContext {
  state: Record<string, unknown>;
  questions: Record<string, ClassifierQuestion>;
}
interface ClassifierResult {
  api: string;
  provider: string;
  model: string;
  answers: Record<string, ClassifierAnswer>;
  /** Set when the service reports token counts. Cost is in USD. */
  usage?: { input: number; output: number; totalTokens: number; cost: { total: number } };
  stopReason: "stop" | "error" | "aborted";
  errorMessage?: string;
  timestamp: number;
}`;
/** Declarations of the `models` globals; codemode-execute.ts implements them. */
export const MODEL_GLOBAL_DECLARATIONS = [
    {
        name: "models.getModelsOfType",
        description: "Every known model of a type, optionally for one provider.",
        signature: "(type: ModelType, provider?: string): Promise<ModelInfo[]>",
    },
    {
        name: "models.getAvailableOfType",
        description: "Models of a type whose provider has working credentials.",
        signature: "(type: ModelType, provider?: string): Promise<ModelInfo[]>",
    },
    {
        name: "models.getModelOfType",
        description: "One catalog entry, or undefined.",
        signature: "(type: ModelType, provider: string, id: string): Promise<ModelInfo | undefined>",
    },
    {
        name: "models.classify",
        description: "Run a classifier model on one state. Only `provider` and `id` of `model` are used. Provider errors do not throw: check `stopReason` and `errorMessage`.",
        signature: "(model: ModelInfo, context: ClassifierContext): Promise<ClassifierResult>",
    },
];
const DEFERRED_TOOLS_GUIDANCE = `Some nested tools may be omitted from this description, such as deferred tools and MCP tools. They are still available on the global \`tools\` object and listed in \`ALL_TOOLS\`.
To find one, call \`await searchTools(query)\` (pass \`{ namespace }\` to search one namespace), or filter \`ALL_TOOLS\` by \`name\` and \`description\`. \`await describeNamespace(name)\` returns a namespace's usage instructions and the names of its tools.`;
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
    const sections = [DESCRIPTION_INTRO, DEFERRED_TOOLS_GUIDANCE];
    if (declarations.some((declaration) => shown.has(declaration.name) && mcpStructuredContentSchema(declaration.outputSchema) !== undefined)) {
        sections.push(`Shared MCP Types:\n\`\`\`ts\n${MCP_TYPESCRIPT_PREAMBLE}\n\`\`\``);
    }
    if (options.models) {
        const noop = () => undefined;
        const models = renderDeclarations({
            globals: MODEL_GLOBAL_DECLARATIONS.map((global) => ({ ...global, execute: noop })),
        });
        sections.push(`Model API:\n\`\`\`ts\n${MODEL_TYPES}\n\n${models}\n\`\`\``);
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
 * How the codemode tool presents tools that are both declared and callable from scripts:
 * - `on`: their descriptions get the codemode declaration appended, and the codemode description
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
                descriptions[tool.name] = renderToolSample(toCodemodeDeclaration(tool));
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