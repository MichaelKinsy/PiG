// Generates cc44fa21_goldens.json: random tool sets, queries and limits ranked by Pi's own tool-search/tool.ts at commit
// cc44fa21 (earendil-works/pi#11331), the first Pi release of the field-weighted ranker PiG carries (D217).
//
//   PI_CHECKOUT=<pi checkout at cc44fa21> node gen_cc44fa21_goldens.mjs > cc44fa21_goldens.json
//
// The pinned Pi (b3a29f11) scores the name, description and namespace as one bag of words, so the Pi oracle cannot supply these.
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const source = readFileSync(join(process.env.PI_CHECKOUT, "packages/coding-agent/src/extensions/tool-search/tool.ts"), "utf8");
const dir = mkdtempSync(join(tmpdir(), "pi-tool-search-"));
// tool.ts imports typebox only to declare the tool's input schema; the ranker does not use it.
writeFileSync(join(dir, "tool.ts"), source.replace('import { type Static, Type } from "typebox";', "const Type = new Proxy({}, { get: () => () => ({}) });"));
const m = await import(join(dir, "tool.ts"));

let seed = 11;
const rand = (n) => {
	seed = (seed * 1664525 + 1013904223) % 4294967296;
	return Math.floor((seed / 4294967296) * n);
};
const words = ["issue", "issues", "search", "searches", "boxes", "class", "status", "the", "and", "of", "GitHub", "getHTTPResponse", "list_issues", "deploy", "release", "app", "apps", "thread", "read", "state", "window", "describe", "peekaboo", "menu", "queue", "id", "URLs", "x-y.z", "UPPER", "Mixed3Case"];
const text = (n, sep) => Array.from({ length: n }, () => words[rand(words.length)]).join(sep ?? " ");
const name = () => {
	const segments = 1 + rand(3);
	return Array.from({ length: segments }, () => text(1 + rand(2), "_")).join("__");
};
const schema = (depth) => {
	const s = {};
	if (rand(2) === 0) s.description = text(1 + rand(3));
	if (depth > 0 && rand(2) === 0) {
		s.properties = {};
		for (let i = 0; i < 1 + rand(3); i++) s.properties[text(1)] = schema(depth - 1);
	}
	return s;
};
const probes = [];
for (let i = 0; i < 300; i++) {
	const tools = [];
	for (let j = 0; j < 1 + rand(10); j++) {
		const tool = { name: name(), description: text(rand(8)), parameters: { type: "object", properties: schema(1).properties ?? {} } };
		if (rand(3) === 0) {
			tool.namespace = { name: text(1), description: rand(2) === 0 ? text(2) : "", instructions: rand(2) === 0 ? text(1 + rand(12)) : "" };
		}
		tools.push(tool);
	}
	// Half of the queries are built from the words of a tool's own name, so whole-name bonuses and exact names occur.
	const query = rand(2) === 0 ? text(rand(5)) : `${text(rand(2))} ${tools[rand(tools.length)].name.split("__").pop().replaceAll("_", rand(2) === 0 ? " " : "_")} ${text(rand(2))}`.trim();
	const limit = [1, 2, 3, 8, 50][rand(5)];
	const documents = tools.map((t) => m.createToolSearchDocument({ name: t.name, description: t.description, parameters: t.parameters }, t.namespace));
	probes.push({ tools, query, limit, ranked: new m.Bm25Ranker().rank(query, documents, limit) });
}
console.log(JSON.stringify(probes));
