// Drive the installed Pi chord keyed binding, never a translated oracle: seeded random provider update sequences delivered to an
// observed keyed service, with the errors, opened observations and member values Pi's binding shows after each update.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createRemoteServiceBinding, defineService } = await import(pathToFileURL(root + "dist/index.js"));
const { BACKGROUND_CONTEXT } = await import(pathToFileURL(root + "dist/context/index.js"));
const { InstanceDirectory } = await import(pathToFileURL(root + "dist/services/instances.js"));
// An observer's context carries no instance address, so the oracle names each opened observation from the directory entry it opens
// for: replace records each entry's address by its service object, and observe's handler runs synchronously inside the binding's own.
const addresses = new WeakMap();
const replace = InstanceDirectory.prototype.replace;
InstanceDirectory.prototype.replace = function (entry) {
	addresses.set(entry.service, `${entry.key}:${entry.generation}`);
	return replace.call(this, entry);
};
let opening;
const observe = InstanceDirectory.prototype.observe;
InstanceDirectory.prototype.observe = function (handler) {
	return observe.call(this, (service, context) => {
		opening = addresses.get(service);
		return handler(service, context);
	});
};
const scenarios = Number(process.argv[3]);
const steps = Number(process.argv[4]);
let seed = Number(process.argv[5]);
const serviceId = process.argv[6];
// mulberry32
const rnd = () => {
	seed = (seed + 0x6d2b79f5) | 0;
	let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
	t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
	return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const pick = (xs) => xs[Math.floor(rnd() * xs.length)];
const int = (n) => Math.floor(rnd() * n);

const service = defineService(serviceId);
const keys = ["k1", "k2", "k3"];
const state = (name, sequence, ops) => ({ name, kind: "state", sequence, ops });
const ops = () =>
	pick([
		[["r", { a: int(9) }]],
		[["s", ["a"], int(9)]],
		[["s", ["b"], "x"], ["d", ["a"]]],
		[["s", ["a", "deep"], 1]],
		[],
		[["r", 7]],
	]);
// last follows the provider's sequence per instance key and member, so most state updates are in order and a few repeat or skip one.
let last = {};
const members = (key) => {
	const list = [];
	const base = (name) => {
		const sequence = int(3);
		last[`${key}.${name}`] = sequence;
		return state(name, sequence, [["r", { a: int(5) }]]);
	};
	if (rnd() < 0.9) list.push(rnd() < 0.9 ? base("s") : rnd() < 0.5 ? { name: "s", kind: "method" } : state("s", 0, ops()));
	if (rnd() < 0.85) list.push(rnd() < 0.9 ? base("t") : { name: "t", kind: "method" });
	if (rnd() < 0.8) list.push({ name: "m", kind: "method" });
	if (rnd() < 0.03) list.push({ name: "", kind: "method" });
	for (let i = list.length - 1; i > 0; i--) {
		const j = int(i + 1);
		[list[i], list[j]] = [list[j], list[i]];
	}
	return list;
};
const address = () => ({ key: pick(keys), generation: 1 + int(3) });
const instance = () => {
	const instance = address();
	return rnd() < 0.04 ? { members: members(instance.key) } : { instance, members: members(instance.key) };
};
const update = () => {
	const r = rnd();
	if (r < 0.5) {
		const member = pick(["s", "s", "s", "t", "t", "m", "x"]);
		const at = address();
		const name = `${at.key}.${member}`;
		const sequence = (last[name] ?? 0) + pick([1, 1, 1, 1, 1, 0, 2]);
		last[name] = sequence;
		return rnd() < 0.04 ? { type: "state", member, sequence, ops: ops() } : { type: "state", instance: at, member, sequence, ops: ops() };
	}
	if (r < 0.62) {
		const instances = Array.from({ length: int(4) }, instance);
		return { type: "reset", snapshot: { serviceId: rnd() < 0.95 ? serviceId : "other", mode: rnd() < 0.95 ? "keyed" : "singleton", instances } };
	}
	if (r < 0.8) return { type: "spawned", instance: instance() };
	if (r < 0.95) return { type: "closed", instance: address() };
	return rnd() < 0.5 ? { type: "unavailable" } : { type: "replaced", snapshot: { members: [] } };
};
const flush = () => new Promise((resolve) => setImmediate(resolve));
const out = [];
for (let s = 0; s < scenarios; s++) {
	last = {};
	let listener;
	const errors = [];
	const opened = [];
	const views = [];
	const initial = [
		{ instance: { key: "k1", generation: 1 }, members: [state("s", 0, [["r", { a: 1 }]]), state("t", 0, [["r", { a: 1 }]]), { name: "m", kind: "method" }] },
		{ instance: { key: "k2", generation: 1 }, members: [state("s", 0, [["r", { a: 2 }]]), { name: "m", kind: "method" }] },
	];
	for (const [key, names] of [["k1", ["s", "t"]], ["k2", ["s"]]]) for (const name of names) last[`${key}.${name}`] = 0;
	const transport = {
		invoke: async () => undefined,
		subscribe: async (id, mode, l) => {
			listener = l;
			return { snapshot: { serviceId: id, mode, instances: initial }, activate() {}, close() {} };
		},
	};
	const binding = createRemoteServiceBinding({ services: [service], transport, onError: (e) => errors.push(e.message) });
	binding.observe(service, (view, context) => {
		const name = opening;
		opened.push(name);
		views.push({ name, view });
	});
	await binding.ready(BACKGROUND_CONTEXT);
	await flush();
	const read = (view, name) => {
		try {
			const value = view[name].value;
			return value === undefined ? "undefined" : JSON.stringify(value);
		} catch (e) {
			return "!" + e.message;
		}
	};
	const updates = [];
	const observed = [];
	const record = () => {
		observed.push({ errors: errors.splice(0), opened: opened.splice(0), values: views.map(({ name, view }) => `${name} s=${read(view, "s")} t=${read(view, "t")}`) });
	};
	record();
	for (let i = 0; i < steps; i++) {
		const u = update();
		updates.push(u);
		listener(u, BACKGROUND_CONTEXT);
		await flush();
		record();
	}
	out.push({ updates, observed });
}
process.stdout.write(JSON.stringify(out));
