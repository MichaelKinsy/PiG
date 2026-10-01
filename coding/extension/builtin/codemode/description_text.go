package codemode

// The model-facing text of the tool, copied from packages/coding-agent/src/extensions/codemode/tool.ts.

// descriptionIntro is DESCRIPTION_INTRO.
const descriptionIntro = "Run JavaScript code to orchestrate/compose tool calls\n" +
	"- Evaluates the provided JavaScript code in a fresh QuickJS sandbox as the body of an async function: top-level `await` and `return` work.\n" +
	"- All nested tools are available on the global `tools` object, for example `await tools.read(...)`. Tool names are exposed as normalized JavaScript identifiers, for example `await tools.mcp__ologs__get_profile(...)`.\n" +
	"- Nested tool methods take an object as their input argument.\n" +
	"- Nested tools return either an object or a string, based on the description.\n" +
	"- A nested tool call that fails, is blocked, or gets invalid arguments rejects with an Error carrying the tool's error text.\n" +
	"- Runs raw JavaScript -- no Node, no file system, no network access, no timers.\n" +
	"- Accepts raw JavaScript source text, not JSON, quoted strings, or markdown code fences.\n" +
	"- You may optionally start the tool input with a first line like `// @options: {\"max_output_tokens\": 1000, \"timeout_ms\": 60000}`.\n" +
	"- `max_output_tokens` sets the token budget for the script's output. Defaults to 10000 tokens.\n" +
	"- `timeout_ms` sets a hard deadline for the whole script. By default there is none.\n" +
	"- When the JS code is fully evaluated, calls that are still running are cancelled and unawaited promises are silently discarded.\n" +
	"- Tool calls are real and have side effects. If the script fails partway, earlier calls are not undone.\n" +
	"- Scripts have a 256 MB memory limit; exceeding it throws `InternalError: out of memory`. Filter or aggregate large data instead of accumulating it.\n" +
	"\n" +
	"- Global helpers:\n" +
	"- `exit()`: Immediately ends the current script successfully (like an early return from the top level).\n" +
	"- `text(value: string | number | boolean | undefined | null)`: Appends a text item. Non-string values are stringified with `JSON.stringify(...)` when possible.\n" +
	"- `image(imageUrlOrItem: string | { image_url: string } | ImageContent)`: Appends an image item. `image_url` should be a base64-encoded `data:` URL. To forward an MCP tool image, pass an individual `ImageContent` block from `result.content`, for example `image(result.content[0])`.\n" +
	"- `store(key: string, value: any)`: stores a serializable value under a string key for later `codemode` calls in the same session. Storing `undefined` deletes the key. Writes are kept only if the script succeeds.\n" +
	"- `load(key: string)`: returns the stored value for a string key, or `undefined` if it is missing.\n" +
	"- `ALL_TOOLS`: metadata for the enabled nested tools as `{ name, description }` entries.\n" +
	"- `searchTools(query: string, options?: { limit?: number; namespace?: string })`: resolves to the nested tools that best match the query (BM25, default limit 8), as `{ name, description }` entries like `ALL_TOOLS`.\n" +
	"- `describeTool(name: string)`: resolves to the description and declaration of a nested tool, or `undefined`.\n" +
	"- `describeNamespace(name: string)`: resolves to `{ name, description?, instructions?, tools }` for a namespace of nested tools, such as an MCP server: its usage instructions and the names of its tools, or `undefined`.\n" +
	"- `console.log(...)` and the other `console` methods append a text item like `text()`.\n" +
	"- `return value` at the top level appends the value like `text()`."

// modelTypes is MODEL_TYPES.
const modelTypes = "type ModelType = \"chat\" | \"image\" | \"classifier\";\n" +
	"/** A model catalog entry. `provider` and `id` identify it; the other fields depend on the type. */\n" +
	"interface ModelInfo {\n" +
	"  type?: ModelType;\n" +
	"  provider: string;\n" +
	"  id: string;\n" +
	"  name: string;\n" +
	"  api: string;\n" +
	"  input: (\"text\" | \"image\")[];\n" +
	"  contextWindow?: number;\n" +
	"  [key: string]: unknown;\n" +
	"}\n" +
	"type ClassifierQuestion =\n" +
	"  | { type: \"choice\"; instructions: string; criteria: Record<string, string> }\n" +
	"  | { type: \"score\"; instructions: string; criteria: string[] }\n" +
	"  | { type: \"bool\"; instructions: string; criteria: { true: string; false: string } };\n" +
	"type ClassifierAnswer =\n" +
	"  | { type: \"choice\"; choice: string; probabilities: Record<string, number>; confidence: number }\n" +
	"  | { type: \"score\"; score: number; confidence: number }\n" +
	"  | { type: \"bool\"; probability: number };\n" +
	"interface ClassifierContext {\n" +
	"  state: Record<string, unknown>;\n" +
	"  questions: Record<string, ClassifierQuestion>;\n" +
	"}\n" +
	"interface ClassifierResult {\n" +
	"  api: string;\n" +
	"  provider: string;\n" +
	"  model: string;\n" +
	"  answers: Record<string, ClassifierAnswer>;\n" +
	"  /** Set when the service reports token counts. Cost is in USD. */\n" +
	"  usage?: { input: number; output: number; totalTokens: number; cost: { total: number } };\n" +
	"  stopReason: \"stop\" | \"error\" | \"aborted\";\n" +
	"  errorMessage?: string;\n" +
	"  timestamp: number;\n" +
	"}"

// deferredToolsGuidance is DEFERRED_TOOLS_GUIDANCE.
const deferredToolsGuidance = "Some nested tools may be omitted from this description, such as deferred tools and MCP tools. They are still available on the global `tools` object and listed in `ALL_TOOLS`.\n" +
	"To find one, call `await searchTools(query)` (pass `{ namespace }` to search one namespace), or filter `ALL_TOOLS` by `name` and `description`. `await describeNamespace(name)` returns a namespace's usage instructions and the names of its tools."
