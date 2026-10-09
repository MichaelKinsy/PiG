// Drive the installed Pi chord wire parsers, never a translated oracle: seeded random mutations of valid service wire values with Pi's verdicts.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const wire = await import(pathToFileURL(root + "dist/services/wire.js"));
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

const address = { key: "k", generation: 1 };
const members = [{ name: "m", kind: "method" }, { name: "s", kind: "state", sequence: 0, ops: [["r", { a: 1 }]] }];
const wireMembers = [{ name: "m", kind: "method" }, { name: "s", kind: "state", sequence: 0, ops: [["r", { a: 1 }]] }];
const seeds = {
	call: [
		{ serviceId: "svc.a", member: "read", args: [1, "x"] },
		{ serviceId: "$chord.service", member: "subscribe", args: ["sub", "svc.a", "keyed"] },
		{ serviceId: "svc.a", member: "read", args: [], instance: address },
	],
	catalogue: [[{ serviceId: "svc.a", mode: "singleton" }, { serviceId: "svc.b", mode: "keyed" }], []],
	snapshot: [
		{ serviceId: "svc.a", mode: "singleton", instances: [{ members }] },
		{ serviceId: "svc.k", mode: "keyed", instances: [{ instance: address, members }] },
	],
	update: [
		{ type: "state", member: "s", sequence: 1, ops: [["s", ["a"], 2]] },
		{ type: "state", instance: address, member: "s", sequence: 2, ops: [["d", ["a"]]] },
		{ type: "reset", snapshot: { serviceId: "svc.a", mode: "singleton", instances: [{ members }] } },
		{ type: "unavailable" },
		{ type: "replaced", snapshot: { members } },
		{ type: "spawned", instance: { instance: address, members } },
		{ type: "closed", instance: address },
	],
	wireUpdate: [
		{ type: "state", member: "s", sequence: 1, ops: [["s", ["a"], 2]] },
		{ type: "state", instance: address, member: "s", sequence: 2, ops: [["#", 0, ["a"]], ["s", 0, 3]] },
		{ type: "reset", snapshot: { serviceId: "svc.a", mode: "singleton", instances: [{ members: wireMembers }] } },
		{ type: "replaced", snapshot: { members: wireMembers } },
		{ type: "spawned", instance: { instance: address, members: wireMembers } },
		{ type: "closed", instance: address },
	],
};
const parsers = {
	call: wire.parseServiceCall,
	catalogue: wire.parseServiceCatalogue,
	snapshot: wire.parseServiceSubscriptionSnapshot,
	wireSnapshot: wire.parseWireServiceSubscriptionSnapshot,
	update: wire.parseServiceProviderUpdate,
	wireUpdate: wire.parseWireServiceProviderUpdate,
};
const replacements = [null, true, 0, -1, 1.5, 2, "", "x", "svc.a", "keyed", "singleton", "state", "method", "r", [], {}, [["r", 1]], [["s", ["a"], 1]], [["x"]], { key: "k", generation: 0 }, { key: "", generation: 1 }, { members: [] }, 9007199254740992];
const extraKeys = ["extra", "instance", "ops", "sequence", "kind", "snapshot", "args"];

const clone = (v) => JSON.parse(JSON.stringify(v));
const slots = (v, prefix = [], out = []) => {
	out.push(prefix);
	if (Array.isArray(v)) v.forEach((x, i) => slots(x, [...prefix, i], out));
	else if (v && typeof v === "object") for (const k of Object.keys(v)) slots(v[k], [...prefix, k], out);
	return out;
};
const parentOf = (v, p) => p.slice(0, -1).reduce((x, k) => x[k], v);
const mutate = (value) => {
	let v = clone(value);
	const steps = Math.floor(rnd() * 3);
	for (let i = 0; i < steps; i++) {
		const all = slots(v);
		const path = pick(all);
		const op = rnd();
		if (path.length === 0) {
			if (op < 0.2) v = clone(pick(replacements));
			continue;
		}
		const parent = parentOf(v, path);
		const last = path[path.length - 1];
		if (op < 0.5) parent[last] = clone(pick(replacements));
		else if (op < 0.75) {
			if (Array.isArray(parent)) parent.splice(last, 1);
			else delete parent[last];
		} else if (parent && typeof parent === "object" && !Array.isArray(parent)) parent[pick(extraKeys)] = clone(pick(replacements));
	}
	return v;
};
const outcome = (f) => {
	try {
		return { ok: JSON.parse(JSON.stringify(f() ?? null)) };
	} catch (e) {
		return { error: e.constructor.name, message: e.message };
	}
};
const cases = [];
for (let i = 0; i < count; i++) {
	const kind = pick(Object.keys(parsers));
	const seedKind = kind === "wireSnapshot" ? "snapshot" : kind;
	const input = mutate(pick(seeds[seedKind]));
	const control = kind === "call" ? outcome(() => wire.decodeServiceControlCall(wire.parseServiceCall(input))) : undefined;
	cases.push({ kind, input, result: outcome(() => parsers[kind](input)), control });
}
process.stdout.write(JSON.stringify(cases));
