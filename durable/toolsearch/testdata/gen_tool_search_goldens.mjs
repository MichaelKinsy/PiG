// Generates tool_search_goldens.json: Pi's own tool_search tool (packages/durable/src/tool-search/tool-search.ts and search.ts) at commit
// c7372af4 (earendil-works/pi#11631), the field-weighted, coverage-first ranking PiG carries (D217), run over seeded random deferred tools and queries.
//
//   PI_CHECKOUT=<dir holding packages/durable/src/search.ts and tool-search/tool-search.ts at c7372af4> \
//   PI_PACKAGES=<.upstream/current/packages> PI_INTEROP_DIR=<durable/interop> node gen_tool_search_goldens.mjs > tool_search_goldens.json
//
// The pinned Pi (b3a29f11) ranks with the name folded into the text, so its oracle (tool_search_oracle.mjs) cannot supply these. The two files of the
// carried commit are laid over a copy of the pinned durable sources, which supply everything the commit did not touch.
import { cpSync, mkdtempSync, realpathSync } from "node:fs";
import module from "node:module";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const overlay = mkdtempSync(join(tmpdir(), "pi-durable-c7372af4-"));
cpSync(join(process.env.PI_PACKAGES, "durable"), join(overlay, "durable"), { recursive: true });
cpSync(join(process.env.PI_CHECKOUT, "packages/durable/src/search.ts"), join(overlay, "durable/src/search.ts"));
cpSync(join(process.env.PI_CHECKOUT, "packages/durable/src/tool-search/tool-search.ts"), join(overlay, "durable/src/tool-search/tool-search.ts"));
const sourceRoot = pathToFileURL(realpathSync(overlay)).href;
const interop = process.env.PI_INTEROP_DIR.replace(/\/?$/, "/");
const parent = pathToFileURL(`${interop}node_modules/@earendil-works/pi-durable/dist/index.js`).href;
module.registerHooks({
	resolve(specifier, context, next) {
		if (context.parentURL?.startsWith(sourceRoot) && !/^(\.|\/|node:|file:|data:)/.test(specifier)) {
			return next(specifier, { ...context, parentURL: parent });
		}
		return next(specifier, context);
	},
});
const toolSearch = await import(pathToFileURL(join(overlay, "durable/src/tool-search/tool-search.ts")).href);
const tool = toolSearch.createToolSearchTool();

let seed = 17;
const rand = (n) => {
	seed = (seed * 1664525 + 1013904223) % 4294967296;
	return Math.floor((seed / 4294967296) * n);
};
const words = ["github", "issue", "issues", "repo", "list", "get", "create", "search", "file", "path", "query", "thread", "read", "queue", "app", "window", "peekaboo", "menu", "see", "image", "deploy", "release", "status", "log", "state"];
const word = () => words[rand(words.length)];
const text = (n) => Array.from({ length: n }, word).join(" ");
const descriptions = ["", "Read a queued message of this thread.", "Read durable state and a paginated timeline from any thread. The default view returns user messages, assistant messages and plans. Continue with afterPosition until hasMore is false.", "Capture the app window and describe the UI elements of the app.", "Take a screenshot of the screen, an app, or a window.", "Deploy status: last deploy, deploy log, deploy errors.", "  \nLeading blank lines\nthen text"];
const schema = (depth) => {
	const properties = {};
	for (let i = 0; i < rand(4); i++) {
		properties[word()] = depth > 0 && rand(3) === 0 ? schema(depth - 1) : { type: "string", ...(rand(2) === 0 ? { description: text(1 + rand(3)) } : {}) };
	}
	return { type: "object", properties, ...(rand(3) === 0 ? { description: text(2) } : {}) };
};
const name = () => {
	const segments = 1 + rand(3);
	return Array.from({ length: segments }, () => Array.from({ length: 1 + rand(2) }, word).join("_")).join("__");
};
const limits = [undefined, 1, 2, 3, 8, 100];
const cases = [];
for (let i = 0; i < 300; i++) {
	const deferred = [];
	const used = new Set();
	for (let j = 0; j < rand(12); j++) {
		const toolName = name();
		if (used.has(toolName)) continue;
		used.add(toolName);
		deferred.push({ name: toolName, description: rand(3) === 0 ? text(rand(8)) : descriptions[rand(descriptions.length)], parameters: schema(2) });
	}
	// Half of the queries are built from the words of a tool's own name, so whole names and exact names occur.
	const query = deferred.length === 0 || rand(2) === 0 ? text(1 + rand(4)) : `${rand(2) === 0 ? word() + " " : ""}${deferred[rand(deferred.length)].name.split("__").pop().replaceAll("_", rand(2) === 0 ? " " : "_")}`;
	const limit = limits[rand(limits.length)];
	const args = limit === undefined ? { query } : { query, limit };
	const result = await tool.execute(args, { agent: async () => ({ deferred }) }, {});
	cases.push({ deferred, query, limit: limit ?? null, result });
}
console.log(JSON.stringify(cases));
