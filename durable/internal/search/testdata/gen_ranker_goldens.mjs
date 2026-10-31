// Generates ranker_goldens.json: random documents, queries and limits ranked by Pi's own durable/src/search.ts at commit
// c7372af4 (earendil-works/pi#11631), the field-weighted, coverage-first ranker PiG carries (D217).
//
//   PI_CHECKOUT=<pi checkout at c7372af4> node gen_ranker_goldens.mjs > ranker_goldens.json
//
// The pinned Pi (b3a29f11) scores the name as part of the text and has no namespace field, so the Pi oracle cannot supply these.
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const { rankDocuments } = await import(pathToFileURL(join(process.env.PI_CHECKOUT, "packages/durable/src/search.ts")).href);

let seed = 31;
const rand = (n) => {
	seed = (seed * 1664525 + 1013904223) % 4294967296;
	return Math.floor((seed / 4294967296) * n);
};
const words = ["issue", "issues", "search", "searches", "boxes", "class", "status", "the", "and", "of", "GitHub", "getHTTPResponse", "list_issues", "deploy", "release", "app", "apps", "thread", "read", "state", "window", "describe", "peekaboo", "menu", "queue", "id", "URLs", "x-y.z", "UPPER", "Mixed3Case", "dev-app", "\u0130stanbul", "\u212aelvin", "x\u{1F600}y"];
const text = (n, sep) => Array.from({ length: n }, () => words[rand(words.length)]).join(sep ?? " ");
const name = () => {
	const segments = 1 + rand(3);
	return Array.from({ length: segments }, () => text(1 + rand(2), "_")).join("__");
};
const probes = [];
for (let i = 0; i < 400; i++) {
	const documents = [];
	for (let j = 0; j < 1 + rand(10); j++) {
		const document = { name: name(), text: text(rand(8)) };
		if (rand(3) === 0) document.namespaceText = [text(1), rand(2) === 0 ? text(2) : "", rand(2) === 0 ? text(1 + rand(12)) : ""].join(" ");
		documents.push(document);
	}
	// Half of the queries are built from the words of a document's own name, so whole-name bonuses and exact names occur.
	const query = rand(2) === 0 ? text(rand(5)) : `${text(rand(2))} ${documents[rand(documents.length)].name.split("__").pop().replaceAll("_", rand(2) === 0 ? " " : "_")} ${text(rand(2))}`.trim();
	const limit = [-1, 0, 1, 2, 3, 8, 50][rand(7)];
	probes.push({ documents, query, limit, ranked: rankDocuments(query, documents, limit) });
}
console.log(JSON.stringify(probes));
