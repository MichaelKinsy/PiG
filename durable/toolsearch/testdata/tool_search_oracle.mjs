// Run Pi's packages/durable/src/tool-search/tool-search.ts (imported as-is with types stripped): the tool_search tool's
// execute over deferred tools, as the harness calls it.
// Op: {deferred: [{name, description, parameters}], query, limit?}   (limit absent: the model gave none)
// stdout: per op {result: {output, details, control?}} or {error: message}; plus the tool's declaration once as `declaration`.
// usage: node tool_search_oracle.mjs <packages dir>   (PI_INTEROP_DIR: a directory whose node_modules holds @earendil-works/pi-durable)
import module from "node:module";
import { pathToFileURL } from "node:url";

const packages = process.argv[2].replace(/\/?$/, "/");
const interop = process.env.PI_INTEROP_DIR.replace(/\/?$/, "/");
const parent = pathToFileURL(`${interop}node_modules/@earendil-works/pi-durable/dist/index.js`).href;
module.registerHooks({
	resolve(specifier, context, next) {
		if (specifier.startsWith("@earendil-works/") || specifier.startsWith("@silvia-odwyer/")) {
			return next(specifier, { ...context, parentURL: parent });
		}
		return next(specifier, context);
	},
});
const toolSearch = await import(pathToFileURL(`${packages}durable/src/tool-search/tool-search.ts`).href);
const tool = toolSearch.createToolSearchTool();
const extension = toolSearch.createToolSearchExtension();
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const out = [];
for (const op of JSON.parse(input)) {
	try {
		const args = op.limit === undefined || op.limit === null ? { query: op.query } : { query: op.query, limit: op.limit };
		const result = await tool.execute(args, { agent: async () => ({ deferred: op.deferred }) }, {});
		out.push({ result });
	} catch (error) {
		out.push({ error: error instanceof Error ? error.message : String(error) });
	}
}
process.stdout.write(
	JSON.stringify({
		declaration: { name: tool.name, description: tool.description, parameters: tool.parameters, callers: tool.callers, replay: tool.replay },
		extension: { name: extension.name, tools: extension.tools.map((each) => each.name) },
		constants: { name: toolSearch.TOOL_SEARCH_TOOL_NAME, limit: toolSearch.DEFAULT_TOOL_SEARCH_LIMIT },
		out,
	}),
);
