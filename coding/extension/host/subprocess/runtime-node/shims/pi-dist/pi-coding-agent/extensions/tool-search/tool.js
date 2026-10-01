/**
 * Tool discovery: a BM25 ranker over tool metadata, shared by `searchTools()` in codemode scripts and
 * the optional `tool_search` tool.
 *
 * `tool_search` searches tools that are not declared to
 * the model (`codemode` and `deferred` exposure) and loads the matches, so they are declared for the
 * next model call. Loading goes through the active tool set, so it is recorded in the transcript
 * like any other tool change and survives `/tree`, resume, and fork on that branch.
 */
import { Type } from "../../../../typebox.mjs";
export const TOOL_SEARCH_TOOL_NAME = "tool_search";
export const DEFAULT_TOOL_SEARCH_LIMIT = 8;
const STOP_WORDS = new Set([
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
    "with",
]);
/** Naive singular form, so `issues` matches `issue` and `searches` matches `search`. */
function stem(term) {
    if (term.length > 4 && term.endsWith("ies"))
        return `${term.slice(0, -3)}y`;
    if (term.length > 4 && /(ches|shes|sses|xes|zes)$/.test(term))
        return term.slice(0, -2);
    if (term.length > 3 && term.endsWith("s") && !term.endsWith("ss"))
        return term.slice(0, -1);
    return term;
}
/** Lowercase terms, split at camelCase boundaries and non-alphanumerics, without stop words. */
export function tokenize(text) {
    return text
        .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
        .replace(/([A-Z]+)([A-Z][a-z])/g, "$1 $2")
        .toLowerCase()
        .split(/[^a-z0-9]+/)
        .filter((term) => term.length > 0 && !STOP_WORDS.has(term))
        .map(stem);
}
function isObject(value) {
    return typeof value === "object" && value !== null && !Array.isArray(value);
}
/** Schema descriptions and property names, recursively. */
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
/**
 * Search text of a tool: the name, the name with `_`
 * as spaces, the description, schema descriptions and property names, and the namespace with its
 * description and instructions.
 */
export function createToolSearchDocument(tool, namespace) {
    const parts = [tool.name, tool.name.replaceAll("_", " "), tool.description];
    schemaText(tool.parameters, parts);
    if (namespace)
        parts.push(namespace.name, namespace.description ?? "", namespace.instructions ?? "");
    return { name: tool.name, text: parts.filter((part) => part.trim()).join(" ") };
}
/** Okapi BM25 with the usual parameters. Ties keep document order. */
export class Bm25Ranker {
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
            const counts = new Map();
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
                const norm = this.k1 * (1 - this.b + (this.b * lengths[index]) / averageLength);
                score += (idf.get(term) ?? 0) * ((count * (this.k1 + 1)) / (count + norm));
            }
            if (score > 0)
                matches.push({ name: document.name, score });
        });
        return matches.sort((a, b) => b.score - a.score).slice(0, limit);
    }
}
export const toolSearchSchema = Type.Object({
    query: Type.String({ description: "Search query for deferred tools." }),
    limit: Type.Optional(Type.Number({ description: `Maximum number of tools to return. Defaults to ${DEFAULT_TOOL_SEARCH_LIMIT}.` })),
});
/** Whether the tool is this `tool_search`, not another extension's tool of the same name. */
export function isToolSearchTool(tool) {
    return tool.name === TOOL_SEARCH_TOOL_NAME && tool.parameters === toolSearchSchema;
}
/** Whether `tool_search` can load a tool with this exposure. */
function isSearchable(exposure) {
    return exposure === "codemode" || exposure === "deferred";
}
/**
 * Rank the searchable tools that are not active yet and activate the matches, so the next model
 * call declares them. Activation is recorded in the transcript like any tool change.
 */
function searchAndLoad(tools, query, limit) {
    const active = tools.getActiveTools();
    const candidates = tools.getAllTools().filter((tool) => isSearchable(tool.exposure) && !active.includes(tool.name));
    const documents = candidates.map((tool) => createToolSearchDocument(tool, tool.namespace));
    const matches = new Bm25Ranker().rank(query, documents, limit);
    if (matches.length > 0)
        tools.setActiveTools([...active, ...matches.map((match) => match.name)]);
    return matches.map((match) => ({
        name: match.name,
        description: candidates.find((tool) => tool.name === match.name)?.description ?? "",
    }));
}
/**
 * The `tool_search` description. It does not list the searchable tools or their namespaces, so it
 * stays the same while tools are registered, for example when MCP servers connect.
 */
export const TOOL_SEARCH_DESCRIPTION = `# Tool discovery\n\nSearches over deferred tool metadata with BM25 and exposes matching tools for the next model call.\n\nSome of the tools, such as tools of MCP servers, may not have been provided to you upfront, and you should use this tool (\`${TOOL_SEARCH_TOOL_NAME}\`) to search for the required tools. For MCP tool discovery, always use \`${TOOL_SEARCH_TOOL_NAME}\`.`;
export function createToolSearchToolDefinition(options = {}) {
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
            const text = tools.length === 0
                ? "No matching tools found."
                : `Loaded ${tools.length} tool${tools.length === 1 ? "" : "s"}. They are available from your next call:\n${tools
                    .map((tool) => `- ${tool.name}: ${tool.description.trim().split(/\r?\n/)[0]}`)
                    .join("\n")}`;
            return { content: [{ type: "text", text }], details: { loaded: tools.map((tool) => tool.name) } };
        },
    };
}
//# sourceMappingURL=tool.js.map