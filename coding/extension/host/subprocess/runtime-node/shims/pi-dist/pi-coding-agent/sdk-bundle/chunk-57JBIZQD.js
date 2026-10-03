import {
  VisualLinePreview,
  getTextOutput,
  replaceTabs,
  str
} from "./chunk-65Z52CAH.js";
import {
  getDocsPath,
  highlightCode,
  keyHint
} from "./chunk-6PEVBP2X.js";
import {
  __name
} from "./chunk-SHUYVCID.js";

// pi-dist/pi-coding-agent/extensions/tool-search/tool.js
import { Type } from "../../../typebox.mjs";
var TOOL_SEARCH_TOOL_NAME = "tool_search";
var DEFAULT_TOOL_SEARCH_LIMIT = 8;
var STOP_WORDS = /* @__PURE__ */ new Set([
  "a",
  "an",
  "and",
  "are",
  "as",
  "at",
  "be",
  "by",
  "for",
  "from",
  "in",
  "is",
  "it",
  "of",
  "on",
  "or",
  "that",
  "the",
  "this",
  "to",
  "with"
]);
function stem(term) {
  if (term.length > 4 && term.endsWith("ies"))
    return `${term.slice(0, -3)}y`;
  if (term.length > 4 && /(ches|shes|sses|xes|zes)$/.test(term))
    return term.slice(0, -2);
  if (term.length > 3 && term.endsWith("s") && !term.endsWith("ss"))
    return term.slice(0, -1);
  return term;
}
__name(stem, "stem");
function tokenize(text) {
  return text.replace(/([a-z0-9])([A-Z])/g, "$1 $2").replace(/([A-Z]+)([A-Z][a-z])/g, "$1 $2").toLowerCase().split(/[^a-z0-9]+/).filter((term) => term.length > 0 && !STOP_WORDS.has(term)).map(stem);
}
__name(tokenize, "tokenize");
function isObject(value) {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
__name(isObject, "isObject");
function schemaText(schema, parts) {
  if (!isObject(schema))
    return;
  if (typeof schema.description === "string")
    parts.push(schema.description);
  if (isObject(schema.properties)) {
    for (const [name, property] of Object.entries(schema.properties)) {
      parts.push(name);
      schemaText(property, parts);
    }
  }
  schemaText(schema.items, parts);
  for (const key of ["anyOf", "oneOf", "allOf"]) {
    const variants = schema[key];
    if (Array.isArray(variants))
      for (const variant of variants)
        schemaText(variant, parts);
  }
}
__name(schemaText, "schemaText");
function createToolSearchDocument(tool, namespace) {
  const parts = [tool.name, tool.name.replaceAll("_", " "), tool.description];
  schemaText(tool.parameters, parts);
  if (namespace)
    parts.push(namespace.name, namespace.description ?? "", namespace.instructions ?? "");
  return { name: tool.name, text: parts.filter((part) => part.trim()).join(" ") };
}
__name(createToolSearchDocument, "createToolSearchDocument");
var Bm25Ranker = class {
  static {
    __name(this, "Bm25Ranker");
  }
  k1;
  b;
  constructor(options = {}) {
    this.k1 = options.k1 ?? 1.2;
    this.b = options.b ?? 0.75;
  }
  rank(query, documents, limit) {
    const queryTerms = [...new Set(tokenize(query))];
    if (queryTerms.length === 0 || documents.length === 0 || limit <= 0)
      return [];
    const termCounts = documents.map((document) => {
      const counts = /* @__PURE__ */ new Map();
      for (const term of tokenize(document.text))
        counts.set(term, (counts.get(term) ?? 0) + 1);
      return counts;
    });
    const lengths = termCounts.map((counts) => [...counts.values()].reduce((sum, count) => sum + count, 0));
    const averageLength = lengths.reduce((sum, length) => sum + length, 0) / documents.length || 1;
    const idf = new Map(queryTerms.map((term) => {
      const frequency = termCounts.filter((counts) => counts.has(term)).length;
      return [term, Math.log(1 + (documents.length - frequency + 0.5) / (frequency + 0.5))];
    }));
    const matches = [];
    documents.forEach((document, index) => {
      let score = 0;
      for (const term of queryTerms) {
        const count = termCounts[index].get(term);
        if (!count)
          continue;
        const norm = this.k1 * (1 - this.b + this.b * lengths[index] / averageLength);
        score += (idf.get(term) ?? 0) * (count * (this.k1 + 1) / (count + norm));
      }
      if (score > 0)
        matches.push({ name: document.name, score });
    });
    return matches.sort((a, b) => b.score - a.score).slice(0, limit);
  }
};
var toolSearchSchema = Type.Object({
  query: Type.String({ description: "Search query for deferred tools." }),
  limit: Type.Optional(Type.Number({ description: `Maximum number of tools to return. Defaults to ${DEFAULT_TOOL_SEARCH_LIMIT}.` }))
});
function isToolSearchTool(tool) {
  return tool.name === TOOL_SEARCH_TOOL_NAME && tool.parameters === toolSearchSchema;
}
__name(isToolSearchTool, "isToolSearchTool");
function isSearchable(exposure) {
  return exposure === "codemode" || exposure === "deferred";
}
__name(isSearchable, "isSearchable");
function searchAndLoad(tools, query, limit) {
  const active = tools.getActiveTools();
  const candidates = tools.getAllTools().filter((tool) => isSearchable(tool.exposure) && !active.includes(tool.name));
  const documents = candidates.map((tool) => createToolSearchDocument(tool, tool.namespace));
  const matches = new Bm25Ranker().rank(query, documents, limit);
  if (matches.length > 0)
    tools.setActiveTools([...active, ...matches.map((match) => match.name)]);
  return matches.map((match) => ({
    name: match.name,
    description: candidates.find((tool) => tool.name === match.name)?.description ?? ""
  }));
}
__name(searchAndLoad, "searchAndLoad");
var TOOL_SEARCH_DESCRIPTION = `# Tool discovery

Searches over deferred tool metadata with BM25 and exposes matching tools for the next model call.

Some of the tools, such as tools of MCP servers, may not have been provided to you upfront, and you should use this tool (\`${TOOL_SEARCH_TOOL_NAME}\`) to search for the required tools. For MCP tool discovery, always use \`${TOOL_SEARCH_TOOL_NAME}\`.`;
function createToolSearchToolDefinition(options = {}) {
  return {
    name: TOOL_SEARCH_TOOL_NAME,
    label: TOOL_SEARCH_TOOL_NAME,
    description: TOOL_SEARCH_DESCRIPTION,
    promptSnippet: "Search for tools that are not loaded yet and load the matches",
    parameters: toolSearchSchema,
    // Searching is not something scripts need; it changes what the model sees.
    exposure: "model-only",
    async execute(_toolCallId, { query, limit }) {
      if (query.trim() === "")
        throw new Error("query must not be empty");
      const max = limit ?? DEFAULT_TOOL_SEARCH_LIMIT;
      if (!Number.isInteger(max) || max <= 0)
        throw new Error("limit must be a positive integer");
      const tools = options.tools ? searchAndLoad(options.tools, query, max) : [];
      const text = tools.length === 0 ? "No matching tools found." : `Loaded ${tools.length} tool${tools.length === 1 ? "" : "s"}. They are available from your next call:
${tools.map((tool) => `- ${tool.name}: ${tool.description.trim().split(/\r?\n/)[0]}`).join("\n")}`;
      return { content: [{ type: "text", text }], details: { loaded: tools.map((tool) => tool.name) } };
    }
  };
}
__name(createToolSearchToolDefinition, "createToolSearchToolDefinition");

// pi-dist/pi-coding-agent/extensions/codemode/tool.js
import { join } from "node:path";
import { MCP_TYPESCRIPT_PREAMBLE, mcpStructuredContentSchema, renderToolOutputType, renderToolSample, toCodemodeIdentifier } from "../../pi-codemode/declarations.js";
import { CODEMODE_SOURCE_GRAMMAR } from "../../pi-codemode/source.js";
import { Type as Type2 } from "../../../typebox.mjs";

// pi-dist/pi-coding-agent/extensions/codemode/execute.lazy.js
var loadCodemodeExecutor = /* @__PURE__ */ __name(() => import("./chunk-QKVFGXHI.js"), "loadCodemodeExecutor");

// pi-dist/pi-coding-agent/extensions/codemode/renderer.js
import { Container, Spacer, Text } from "../../../pi-tui.mjs";
var CODE_PREVIEW_LINES = 10;
var CALL_PREVIEW_COUNT = 8;
var OUTPUT_PREVIEW_LINES = 5;
var COLLAPSED_ARGS_CHARS = 80;
var SCRIPT_HEADER = /^Script (completed|failed)\nWall time [\d.]+ seconds\nOutput:\n$/;
function expandHint(theme, hidden, noun) {
  return `${theme.fg("muted", `... (${hidden} more ${noun},`)} ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`;
}
__name(expandHint, "expandHint");
function formatDuration(ms) {
  if (ms === void 0)
    return "";
  return ms < 1e3 ? `${Math.round(ms)}ms` : `${(ms / 1e3).toFixed(1)}s`;
}
__name(formatDuration, "formatDuration");
function formatCost(cost) {
  return `$${cost >= 0.01 ? cost.toFixed(2) : cost.toPrecision(2)}`;
}
__name(formatCost, "formatCost");
function statusIcon(call, theme) {
  switch (call.status) {
    case "running":
      return theme.fg("warning", "\u2026");
    case "ok":
      return theme.fg("success", "\u2713");
    case "error":
      return theme.fg("error", "\u2717");
    case "cancelled":
      return theme.fg("muted", "\u2298");
  }
}
__name(statusIcon, "statusIcon");
function formatCall(call, theme, expanded) {
  const args = !expanded && call.args.length > COLLAPSED_ARGS_CHARS ? `${call.args.slice(0, COLLAPSED_ARGS_CHARS - 3)}...` : call.args;
  const duration = formatDuration(call.durationMs);
  let line = `${statusIcon(call, theme)} ${theme.fg("toolTitle", call.name)}`;
  if (args)
    line += ` ${theme.fg("muted", args)}`;
  if (duration)
    line += ` ${theme.fg("dim", duration)}`;
  if (call.cost)
    line += ` ${theme.fg("dim", formatCost(call.cost))}`;
  if (expanded && call.error)
    line += `
    ${theme.fg("error", call.error.split("\n").join("\n    "))}`;
  return line;
}
__name(formatCall, "formatCall");
var codemodeRenderers = {
  renderCall(args, theme, context) {
    const code = str(args?.code);
    const title = theme.fg("toolTitle", theme.bold("codemode"));
    const component = context.lastComponent ?? new Container();
    component.clear();
    if (code === null) {
      component.addChild(new Text(`${title} ${theme.fg("error", "[invalid arg]")}`, 0, 0));
      return component;
    }
    component.addChild(new Text(title, 0, 0));
    if (code) {
      const highlighted = highlightCode(replaceTabs(code.replace(/\r/g, "").trimEnd()), "javascript").join("\n");
      component.addChild(context.expanded ? new Text(highlighted, 0, 0) : new VisualLinePreview({
        text: highlighted,
        maxVisualLines: CODE_PREVIEW_LINES,
        keep: "start",
        formatHint: /* @__PURE__ */ __name((hidden) => expandHint(theme, hidden, "lines"), "formatHint")
      }));
    }
    return component;
  },
  renderResult(result, options, theme, context) {
    const component = context.lastComponent ?? new Container();
    component.clear();
    const calls = result.details?.calls ?? [];
    if (calls.length > 0) {
      const shown = options.expanded ? calls : calls.slice(-CALL_PREVIEW_COUNT);
      const lines = shown.map((call) => formatCall(call, theme, options.expanded));
      if (shown.length < calls.length) {
        lines.unshift(`${theme.fg("muted", `... (${calls.length - shown.length} earlier calls,`)} ${keyHint("app.tools.expand", "to expand")}${theme.fg("muted", ")")}`);
      }
      const priced = calls.filter((call) => call.cost);
      if (priced.length > 1) {
        const total = priced.reduce((sum, call) => sum + (call.cost ?? 0), 0);
        lines.push(theme.fg("muted", `Model calls: ${formatCost(total)}`));
      }
      component.addChild(new Spacer(1));
      component.addChild(new Text(lines.join("\n"), 0, 0));
    }
    const [first, ...rest] = result.content;
    const hasHeader = first?.type === "text" && SCRIPT_HEADER.test(first.text);
    const output = options.isPartial ? "" : getTextOutput({ ...result, content: hasHeader ? rest : result.content }, context.showImages).trim();
    if (output) {
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
          formatHint: /* @__PURE__ */ __name((hidden) => expandHint(theme, hidden, "lines"), "formatHint")
        }));
        const fullOutputPath = result.details?.fullOutputPath;
        if (fullOutputPath)
          component.addChild(new Text(theme.fg("muted", `Full output: ${fullOutputPath}`), 0, 0));
      }
    }
    return component;
  }
};

// pi-dist/pi-coding-agent/extensions/codemode/tool.js
var CODEMODE_TOOL_NAME = "codemode";
var CODEMODE_STORE_ENTRY_TYPE = "codemode-store";
var TEXT_OUTPUT_SCHEMA = { type: "string" };
var codemodeSchema = Type2.Object({
  code: Type2.String({
    description: "Raw JavaScript source."
  })
});
function isCodemodeTool(tool) {
  return tool.name === CODEMODE_TOOL_NAME && tool.parameters === codemodeSchema;
}
__name(isCodemodeTool, "isCodemodeTool");
var codemodeToolSystemPromptContribution = {
  snippet: "Run JavaScript that calls other tools",
  guidelines: [
    "Use codemode to batch independent tool calls (Promise.allSettled), chain them, or filter large output, instead of many separate calls."
  ]
};
var CODEMODE_DOCS_PATH = join(getDocsPath(), "codemode.md");
var DESCRIPTION_INTRO = `Run JavaScript that calls other tools. The input is raw JavaScript (not JSON, no code fence), run as an async function body in a QuickJS sandbox: top-level \`await\` and \`return\` work. No Node, file system, network, or timers.
- \`await tools.<name>({ ...args })\` resolves to a string, or an object if the tool's declaration says so, and rejects with an Error on failure. Calls still running when the script ends are cancelled.
- Optional first line: \`// @options: {"max_output_tokens": 10000, "timeout_ms": 60000}\``;
function describeGlobals(models) {
  const lines = [
    "Globals:",
    "- `text(value)`, `image(dataUrlOrImageBlock)`, `console.log(...)`, and top-level `return` add output; `exit()` ends the script.",
    "- `store(key, value)` and `load(key)` keep JSON values across codemode calls.",
    "- `ALL_TOOLS`, `searchTools(query, { limit?, namespace? })`, `describeTool(name)`, `describeNamespace(name)`: find unlisted tools, such as MCP tools."
  ];
  if (models) {
    lines.push(`- \`models\`: classifiers and image generation. Read ${CODEMODE_DOCS_PATH} first.`);
  }
  return lines.join("\n");
}
__name(describeGlobals, "describeGlobals");
var DEFAULT_CODEMODE_INLINE_BUDGET = 3e3;
var CHARS_PER_TOKEN = 4;
function toCodemodeDeclaration(tool) {
  return {
    name: tool.name,
    description: tool.description,
    inputSchema: tool.parameters,
    outputSchema: tool.outputSchema ?? TEXT_OUTPUT_SCHEMA
  };
}
__name(toCodemodeDeclaration, "toCodemodeDeclaration");
function getCodemodeCallableTools(tools) {
  return tools.filter((tool) => tool.name !== CODEMODE_TOOL_NAME);
}
__name(getCodemodeCallableTools, "getCodemodeCallableTools");
function renderToolSection(declaration) {
  const id = toCodemodeIdentifier(declaration.name);
  const heading = id === declaration.name ? `### \`${id}\`` : `### \`${id}\` (\`${declaration.name}\`)`;
  return `${heading}
${renderToolSample(declaration).trim()}`;
}
__name(renderToolSection, "renderToolSection");
function selectCatalog(groups, budget) {
  if (budget === void 0)
    return new Set(groups.flatMap((group) => group.entries.map((entry) => entry.name)));
  const queues = groups.map((group) => [...group.entries].sort((a, b) => a.cost - b.cost));
  const shown = /* @__PURE__ */ new Set();
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
__name(selectCatalog, "selectCatalog");
function createCodemodeDescription(tools, options = {}) {
  const declarations = getCodemodeCallableTools(tools).filter((tool) => !options.deferred?.has(tool.name)).map(toCodemodeDeclaration);
  const groups = /* @__PURE__ */ new Map([["", { namespace: void 0, entries: [] }]]);
  for (const declaration of declarations) {
    const namespace = options.namespaces?.get(declaration.name);
    const key = namespace ? `ns:${namespace.name}` : "";
    const group = groups.get(key) ?? { namespace, entries: [] };
    groups.set(key, group);
    const section = renderToolSection(declaration);
    group.entries.push({ name: declaration.name, section, cost: Math.ceil(section.length / CHARS_PER_TOKEN) });
  }
  const ordered = [...groups.values()].sort((a, b) => a.namespace === void 0 ? -1 : b.namespace === void 0 ? 1 : a.namespace.name.localeCompare(b.namespace.name));
  const shown = selectCatalog(ordered, options.inlineBudget);
  const sections = [DESCRIPTION_INTRO, describeGlobals(options.models === true)];
  if (declarations.some((declaration) => shown.has(declaration.name) && mcpStructuredContentSchema(declaration.outputSchema) !== void 0)) {
    sections.push(`Shared MCP Types:
\`\`\`ts
${MCP_TYPESCRIPT_PREAMBLE}
\`\`\``);
  }
  if (declarations.length === 0)
    return sections.join("\n\n");
  const toolSections = ["Nested tools:"];
  for (const { namespace, entries } of ordered) {
    const visible = entries.filter((entry) => shown.has(entry.name));
    if (namespace) {
      const listing = visible.length === entries.length ? "" : visible.length === 0 ? " (tools not listed)" : " (some tools not listed)";
      const description = namespace.description?.trim();
      toolSections.push(`## ${namespace.name}${listing}${description ? `
${description}` : ""}`);
    }
    for (const entry of visible)
      toolSections.push(entry.section);
  }
  sections.push(toolSections.join("\n\n"));
  return sections.join("\n\n");
}
__name(createCodemodeDescription, "createCodemodeDescription");
function describeOutput(schema) {
  const type = renderToolOutputType(schema);
  if (type === "string")
    return "a string";
  const object = typeof schema === "object" ? schema : void 0;
  const properties = object?.properties;
  if (object?.type === "object" && typeof properties === "object" && properties !== null && mcpStructuredContentSchema(schema) === void 0) {
    const required = new Set(Array.isArray(object.required) ? object.required : []);
    const fields = Object.keys(properties).map((name) => required.has(name) ? name : `${name}?`);
    return `\`{ ${fields.join(", ")} }\``;
  }
  return `\`${type.replace(/\s+/g, " ")}\``;
}
__name(describeOutput, "describeOutput");
function describeScriptCall(tool) {
  return `${tool.description.trim()}

Codemode: \`tools.${toCodemodeIdentifier(tool.name)}(args)\` resolves to ${describeOutput(toCodemodeDeclaration(tool).outputSchema)}.`;
}
__name(describeScriptCall, "describeScriptCall");
function prepareCodemodeLoadout(loadout, options) {
  const mode = options.getMode?.() ?? "on";
  const isDirect = /* @__PURE__ */ __name((tool) => loadout.getExposure(tool.name) === "direct", "isDirect");
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
    inlineBudget: options.getInlineBudget?.() ?? DEFAULT_CODEMODE_INLINE_BUDGET
  });
  const declaredNames = new Set(loadout.declared.map((tool) => tool.name));
  return {
    descriptions,
    hiddenDeclarations: mode === "only" ? callable.filter((tool) => isDirect(tool) && declaredNames.has(tool.name)).map((tool) => tool.name) : []
  };
}
__name(prepareCodemodeLoadout, "prepareCodemodeLoadout");
function createCodemodeToolDefinition(options = {}) {
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
    prepareLoadout: /* @__PURE__ */ __name((loadout) => prepareCodemodeLoadout(loadout, options), "prepareLoadout"),
    // Capable models write the script as raw text instead of a JSON-escaped string.
    constrainedSampling: { type: "grammar", variants: { openai_lark: CODEMODE_SOURCE_GRAMMAR } },
    // The sandbox (worker, QuickJS wasm) loads on the first call, not at startup.
    execute: /* @__PURE__ */ __name(async (toolCallId, params, signal, onUpdate, ctx) => (await loadCodemodeExecutor()).executeCodemode(toolCallId, params, signal, onUpdate, ctx, options), "execute"),
    ...codemodeRenderers
  };
}
__name(createCodemodeToolDefinition, "createCodemodeToolDefinition");

export {
  TOOL_SEARCH_TOOL_NAME,
  DEFAULT_TOOL_SEARCH_LIMIT,
  createToolSearchDocument,
  Bm25Ranker,
  isToolSearchTool,
  createToolSearchToolDefinition,
  CODEMODE_TOOL_NAME,
  CODEMODE_STORE_ENTRY_TYPE,
  isCodemodeTool,
  CODEMODE_DOCS_PATH,
  toCodemodeDeclaration,
  getCodemodeCallableTools,
  createCodemodeToolDefinition
};
