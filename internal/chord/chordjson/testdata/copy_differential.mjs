// Drive the installed Pi chord copyJson, never a translated oracle: seeded random JSON texts and invalid property lists with Pi's results.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { copyJson } = await import(pathToFileURL(root + "dist/json.js"));
const count = Number(process.argv[3]);
let seed = Number(process.argv[4]);
// mulberry32
const rnd = () => {
	seed = (seed + 0x6d2b79f5) | 0;
	let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
	t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
	return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const pick = (xs) => xs[Math.floor(rnd() * xs.length)];
const keys = ["a", "b", "z", "A", "0", "1", "2", "10", "01", "-1", "4294967294", "4294967295", "1.5", "", "__proto__", "é", "key"];

// value builds random JSON text, with objects whose keys appear in random order and may repeat.
const value = (depth) => {
	const roll = rnd();
	if (depth > 2 || roll < 0.3) return JSON.stringify(pick([null, true, false, 0, -1.5, 1e21, "s", "é", 12]));
	if (roll < 0.5) return "[" + Array.from({ length: Math.floor(rnd() * 3) }, () => value(depth + 1)).join(",") + "]";
	const n = Math.floor(rnd() * 6);
	return "{" + Array.from({ length: n }, () => JSON.stringify(pick(keys)) + ":" + value(depth + 1)).join(",") + "}";
};

const kinds = ["number", "nan", "infinity", "function", "date", "map", "string"];
const make = (kind) => ({ number: 1, nan: NaN, infinity: -Infinity, function: () => 1, date: new Date(0), map: new Map(), string: "s" })[kind];

const out = [];
for (let index = 0; index < count; index++) {
	if (rnd() < 0.5) {
		const text = value(0);
		const parsed = JSON.stringify(JSON.parse(text));
		out.push({ kind: "text", text, parsed, copied: JSON.stringify(copyJson(JSON.parse(text))) });
		continue;
	}
	// Assigning "__proto__" sets the prototype rather than an own property, so the property lists leave it out.
	const properties = Array.from({ length: 1 + Math.floor(rnd() * 5) }, () => [pick(keys.filter((key) => key !== "__proto__")), pick(kinds)]);
	const object = {};
	for (const [key, kind] of properties) object[key] = make(kind);
	let message = "";
	try {
		copyJson(object);
	} catch (error) {
		message = error.message;
	}
	out.push({ kind: "properties", properties, message });
}
process.stdout.write(JSON.stringify(out));
