// Regenerates pi_description.json: Pi's packages/chord/src/contract.ts DescriptionSchema checked with schema.ts isValid over the
// descriptions below. Run from the repository root with Node 24 or later:
//
//	node chord/host/testdata/pi_description.mjs > chord/host/testdata/pi_description.json
//
// cases are a valid description of each shape (remote with a named and a family placement, local, every member kind) and seeded
// edits of them (a member dropped, replaced by a value of another type, a number off by one, an unknown member added). Every case
// is JSON text and the verdict Pi's isValid gave it; the Go test replays the text through chord/host.
import { DescriptionSchema } from "../../../.upstream/current/packages/chord/src/contract.ts";
import { isValid } from "../../../.upstream/current/packages/chord/src/schema.ts";

function mulberry32(seed) {
	let state = seed >>> 0;
	return () => {
		state = (state + 0x6d2b79f5) >>> 0;
		let t = state;
		t = Math.imul(t ^ (t >>> 15), t | 1);
		t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
		return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
	};
}

const members = { m: { kind: "method" }, n: { kind: "method", args: { type: "object" }, result: { type: "string" } }, s: { kind: "state", schema: { type: "number" } }, o: { kind: "opaque", id: "x@1" } };
const bases = [
	{ name: "thing", scope: "remote", version: 1, input: { type: "object" }, host: { name: "h" }, variants: { main: members } },
	{ name: "chat", scope: "remote", version: 2, input: { type: "object", properties: { session: { type: "string" } } }, host: { prefix: "session:", key: "session" }, variants: { viewer: { s: members.s }, participant: { s: members.s, n: members.n } } },
	{ name: "local", scope: "local", version: 3, input: {}, variants: { "": members } },
	{ name: "bare", scope: "local", version: 0, input: {}, variants: {} },
];

const random = mulberry32(20261102);
const pick = (list) => list[Math.floor(random() * list.length)];
const WRONG = [null, "x", "", 0, -1, 1, 1.5, true, false, [], {}, [1], { a: 1 }, 1e21, { kind: "state" }, { kind: "method" }, { name: "n" }, { prefix: "p", key: "k" }, { name: "n", extra: 1 }, { prefix: "", key: "k" }];
function paths(value, prefix = []) {
	const out = [prefix];
	if (value !== null && typeof value === "object" && !Array.isArray(value)) {
		for (const key of Object.keys(value)) if (prefix.length < 4) out.push(...paths(value[key], [...prefix, key]));
	}
	return out;
}
function edit(value) {
	const copy = JSON.parse(JSON.stringify(value));
	const options = paths(copy).filter((p) => p.length > 0);
	const path = pick(options);
	let target = copy;
	for (const key of path.slice(0, -1)) target = target[key];
	const last = path[path.length - 1];
	const roll = random();
	if (roll < 0.25) delete target[last];
	else if (roll < 0.65) target[last] = pick(WRONG);
	else if (roll < 0.8 && typeof target[last] === "number") target[last] += pick([-1, 1, 0.5]);
	else target.extra = pick(WRONG);
	return copy;
}
const cases = [];
const seen = new Set();
const add = (value) => {
	const text = JSON.stringify(value);
	if (text === undefined || seen.has(text)) return;
	seen.add(text);
	cases.push({ text, expect: isValid(DescriptionSchema, JSON.parse(text)) });
};
for (const base of bases) add(base);
for (const value of [null, 1, "x", [], [{}], {}, { name: "x" }]) add(value);
for (let index = 0; index < 3000; index++) {
	let value = edit(pick(bases));
	if (random() < 0.3) value = edit(value);
	add(value);
}
process.stdout.write(`${JSON.stringify({ cases }, null, "\t")}\n`);
