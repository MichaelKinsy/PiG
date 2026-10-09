// Drive the installed Pi chord mutable replicated state, never a translated oracle: seeded random sequences of change (with draft
// edits, a reentrant change or replace from inside the callback, or a throwing callback), replace, subscribe (with a listener that may
// change the state from inside its delivery) and unsubscribe. After each step the oracle records its result, every listener delivery,
// every provider update for the state published as service member s, and the state's value. The steps are written out so the Go
// test replays the same sequence.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { defineService, replicatedState } = await import(pathToFileURL(root + "dist/index.js"));
const { RemoteServiceProvider } = await import(pathToFileURL(root + "dist/services/provider.js"));
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
// Keys a, b and c hold numbers or are absent; o always holds an object, so a nested edit is always valid.
const edit = () => (rnd() < 0.2 ? ["nest", int(4)] : rnd() < 0.7 ? ["set", pick(["a", "b", "c"]), int(4)] : ["del", pick(["a", "b", "c"])]);
const value = () => {
	const next = {};
	for (const key of ["a", "b", "c"]) if (rnd() < 0.6) next[key] = int(4);
	next.o = { x: int(4) };
	return next;
};
const apply = (draft, [kind, key, n]) => {
	if (kind === "nest") draft.o.x = key;
	else if (kind === "set") draft[key] = n;
	else delete draft[key];
};

const out = [];
for (let scenario = 0; scenario < scenarios; scenario++) {
	const state = replicatedState({ a: 0, o: { x: 0 } });
	const provider = new RemoteServiceProvider([service]);
	provider.provide(service, { s: state });
	let events = [];
	const subscription = provider.subscribe(serviceId, "singleton", (update) => {
		if (update.type === "state") events.push(`P ${update.sequence} ${JSON.stringify(update.ops)}`);
	});
	subscription.activate();
	const run = (fn) => {
		try {
			fn();
			return "ok";
		} catch (error) {
			return "!" + error.message;
		}
	};
	const unsubscribers = [];
	const ops = [];
	const observed = [];
	for (let step = 0; step < steps; step++) {
		events = [];
		const r = rnd();
		let op;
		let result;
		if (r < 0.45) {
			op = { op: "change", edits: Array.from({ length: int(4) }, edit), nested: rnd() < 0.15 ? pick(["change", "replace"]) : "", throws: rnd() < 0.1 };
			result = run(() =>
				state.change(BACKGROUND_CONTEXT, (draft) => {
					for (const e of op.edits) apply(draft, e);
					if (op.nested === "change") events.push(`nested ${run(() => state.change(BACKGROUND_CONTEXT, (inner) => apply(inner, ["set", "a", 9])))}`);
					if (op.nested === "replace") events.push(`nested ${run(() => state.replace(BACKGROUND_CONTEXT, { o: { x: 9 } }))}`);
					if (op.throws) throw new Error("mutate failed");
				}),
			);
		} else if (r < 0.65) {
			op = { op: "replace", value: rnd() < 0.2 ? structuredClone(state.value) : value() };
			result = run(() => state.replace(BACKGROUND_CONTEXT, op.value));
		} else if (r < 0.88) {
			const index = unsubscribers.length;
			op = { op: "subscribe", react: rnd() < 0.4 ? { at: 1 + int(3), edit: edit() } : null };
			let received = 0;
			unsubscribers.push(
				state.subscribe((current, _context, delivery) => {
					received += 1;
					events.push(`L${index} ${delivery.kind} ${delivery.sequence} ${JSON.stringify(current)}`);
					if (op.react?.at === received) events.push(`L${index} reacts ${run(() => state.change(BACKGROUND_CONTEXT, (draft) => apply(draft, op.react.edit)))}`);
				}),
			);
			result = "ok";
		} else if (unsubscribers.length > 0) {
			op = { op: "unsubscribe", index: int(unsubscribers.length) };
			unsubscribers[op.index]();
			result = "ok";
		} else {
			op = { op: "noop" };
			result = "ok";
		}
		ops.push(op);
		observed.push({ result, events, value: JSON.stringify(state.value) });
	}
	out.push({ ops, observed });
}
process.stdout.write(JSON.stringify(out));
