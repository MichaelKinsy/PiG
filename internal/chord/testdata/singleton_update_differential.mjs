// Drive the installed Pi chord singleton binding, never a translated oracle: seeded random provider update sequences delivered to a
// subscribed singleton, with the errors, member values and subscriber deliveries Pi's binding shows after each update.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createRemoteServiceBinding, defineService } = await import(pathToFileURL(root + "dist/index.js"));
const { BACKGROUND_CONTEXT } = await import(pathToFileURL(root + "dist/context/index.js"));
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
const address = { key: "k", generation: 1 };
const state = (name, sequence, ops) => ({ name, kind: "state", sequence, ops });
const base = (name) => state(name, int(3), [["r", { a: int(5) }]]);
const ops = () =>
	pick([
		[["r", { a: int(9) }]],
		[["s", ["a"], int(9)]],
		[["s", ["b"], "x"], ["d", ["a"]]],
		[["d", ["a"]]],
		[["s", ["a", "deep"], 1]],
		[],
		[["r", 7]],
		[["s", ["a"], 1], ["r", { z: 1 }]],
	]);
const members = () => {
	const list = [];
	if (rnd() < 0.9) list.push(rnd() < 0.85 ? base("s") : rnd() < 0.5 ? { name: "s", kind: "method" } : state("s", 0, ops()));
	if (rnd() < 0.8) list.push(rnd() < 0.9 ? base("t") : { name: "t", kind: "method" });
	if (rnd() < 0.8) list.push(rnd() < 0.9 ? { name: "m", kind: "method" } : base("m"));
	if (rnd() < 0.05) list.push(base("s"));
	if (rnd() < 0.03) list.push({ name: "", kind: "method" });
	for (let i = list.length - 1; i > 0; i--) {
		const j = int(i + 1);
		[list[i], list[j]] = [list[j], list[i]];
	}
	return list;
};
const instance = (keyed) => (keyed ? { instance: address, members: members() } : { members: members() });
// last follows the provider's sequence per member, so most state updates are in order and a few repeat or skip one.
let last = {};
const remember = (snapshotInstances) => {
	last = {};
	for (const inst of snapshotInstances) for (const m of inst.members) if (m.kind === "state") last[m.name] = m.sequence;
};
const update = () => {
	const r = rnd();
	if (r < 0.6) {
		const member = pick(["s", "s", "s", "t", "t", "m", "x"]);
		const sequence = (last[member] ?? 0) + pick([1, 1, 1, 1, 1, 0, 2]);
		last[member] = sequence;
		const u = { type: "state", member, sequence, ops: ops() };
		if (rnd() < 0.05) u.instance = address;
		return u;
	}
	if (r < 0.75) {
		const count = pick([1, 1, 1, 1, 0, 2]);
		const instances = Array.from({ length: count }, () => instance(rnd() < 0.05));
		remember(instances);
		return { type: "reset", snapshot: { serviceId: rnd() < 0.95 ? serviceId : "other", mode: rnd() < 0.95 ? "singleton" : "keyed", instances } };
	}
	if (r < 0.8) return { type: "unavailable" };
	if (r < 0.95) {
		const snapshot = instance(rnd() < 0.05);
		remember([snapshot]);
		return { type: "replaced", snapshot };
	}
	return rnd() < 0.5 ? { type: "spawned", instance: instance(true) } : { type: "closed", instance: address };
};
const flush = () => new Promise((resolve) => setImmediate(resolve));
const out = [];
for (let s = 0; s < scenarios; s++) {
	last = { s: 0, t: 0 };
	let listener;
	const errors = [];
	const deliveries = [];
	const transport = {
		invoke: async () => undefined,
		subscribe: async (id, mode, l) => {
			listener = l;
			return {
				snapshot: { serviceId: id, mode, instances: [{ members: [state("s", 0, [["r", { a: 0 }]]), state("t", 0, [["r", { a: 0 }]]), { name: "m", kind: "method" }] }] },
				activate() {},
				close() {},
			};
		},
	};
	const binding = createRemoteServiceBinding({ services: [service], transport, onError: (e) => errors.push(e.message) });
	const handle = binding.use(service);
	handle.s.subscribe((value, _context, delivery) => deliveries.push(`${delivery.kind}:${delivery.sequence}:${JSON.stringify(value)}`));
	await binding.ready(BACKGROUND_CONTEXT);
	await flush();
	const read = (name) => {
		try {
			const value = handle[name].value;
			return value === undefined ? "undefined" : JSON.stringify(value);
		} catch (e) {
			return "!" + e.message;
		}
	};
	const updates = [];
	const observed = [];
	const record = () => {
		observed.push({ errors: errors.splice(0), deliveries: deliveries.splice(0), s: read("s"), t: read("t") });
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
