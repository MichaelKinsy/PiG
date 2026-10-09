// Drive the installed Pi chord RemoteServiceProvider, never a translated oracle: seeded random sequences of provider operations
// (provide, withdraw, replace, validateReplacement, use, spawn, instance close, state publication, subscribe, activate, subscription
// close, invoke, dispose) with each operation's result and the updates every subscriber listener received during it. The operations
// are written out so the Go test replays the same sequence.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { defineService } = await import(pathToFileURL(root + "dist/index.js"));
const { RemoteServiceProvider } = await import(pathToFileURL(root + "dist/services/provider.js"));
const { attachReplicatedStateSource } = await import(pathToFileURL(root + "dist/services/state.js"));
const { BACKGROUND_CONTEXT } = await import(pathToFileURL(root + "dist/context/index.js"));
const scenarios = Number(process.argv[3]);
const steps = Number(process.argv[4]);
let seed = Number(process.argv[5]);
const prefix = process.argv[6];
// mulberry32
const rnd = () => {
	seed = (seed + 0x6d2b79f5) | 0;
	let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
	t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
	return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const pick = (xs) => xs[Math.floor(rnd() * xs.length)];
const int = (n) => Math.floor(rnd() * n);

const services = { s: defineService(`${prefix}.s`), k: defineService(`${prefix}.k`), x: defineService(`${prefix}.x`) };
const describe = (error) => {
	if (error instanceof AggregateError) return `agg ${error.message} [${error.errors.map(describe).join("; ")}]`;
	if (typeof error?.code === "string") return `${error.code} ${error.message}`;
	return `- ${error?.message}`;
};
const shapes = [["s", "t", "m"], ["s", "t", "m"], ["s", "t", "m"], ["s", "m"], ["m", "t", "s"], ["t"], []];

// Op generation runs alongside execution so the generator can follow live state (state values, closer and subscription counts).
const out = [];
for (let scenario = 0; scenario < scenarios; scenario++) {
	const provider = new RemoteServiceProvider([
		{ service: services.s, mode: "singleton" },
		{ service: services.k, mode: "keyed" },
	]);
	const states = []; // { push, value, cursor }
	const implementations = [];
	const closers = [];
	const subscriptions = [];
	let events = [];
	// recent holds the states of the implementation last provided, replaced or spawned successfully, so bursts reach live members.
	let recent = [];
	let created = [];
	const newImplementation = (shape) => {
		created = [];
		const id = implementations.length;
		const implementation = {};
		for (const name of shape) {
			if (name === "m") {
				implementation.m = (...args) => `i${id}.m(${args.length - 1}:${args.slice(0, -1).join(",")})`;
				continue;
			}
			const index = states.length;
			created.push(index);
			const record = { push: undefined, value: { a: index }, cursor: 0 };
			states.push(record);
			implementation[name] = attachReplicatedStateSource(
				{
					attach: () => ({
						snapshot: { value: record.value, cursor: 0 },
						activate: (push) => {
							record.push = push;
						},
						dispose() {},
					}),
				},
				{ onError: (error) => events.push(`state${index} ${describe(error)}`) },
			);
		}
		implementations.push(implementation);
		return implementation;
	};
	const idOf = (implementation) => implementations.indexOf(implementation);
	const run = (fn) => {
		try {
			const result = fn();
			return result === undefined ? "ok" : result;
		} catch (error) {
			return "!" + describe(error);
		}
	};
	// publishing is the state whose source frame is being delivered, so a reaction can publish to it reentrantly.
	let publishing;
	const publish = (index) => {
		const record = states[index];
		const choice = int(3);
		const ops = choice === 0 ? [["r", { a: int(9) }]] : choice === 1 ? [["s", ["a"], int(9)]] : [["s", ["b"], "x"]];
		record.value = choice === 0 ? ops[0][1] : { ...record.value, [ops[0][1][0]]: ops[0][2] };
		record.cursor += 1;
		const outer = publishing;
		publishing = index;
		try {
			record.push({ cursor: record.cursor, value: record.value, ops, context: BACKGROUND_CONTEXT });
		} finally {
			publishing = outer;
		}
		return ops;
	};
	const ops = [];
	const observed = [];
	for (let step = 0; step < steps; step++) {
		// The first steps provide, spawn, subscribe and activate, so most scenarios carry live subscribers.
		const r = step < 6 ? [0, 0.25, 0.65, 0.65, 0.75, 0.75][step] : rnd();
		let op;
		let result;
		events = [];
		if (r < 0.08) {
			op = { op: "provide", shape: step === 0 ? shapes[0] : pick(shapes) };
			result = run(() => provider.provide(services.s, newImplementation(op.shape)));
		} else if (r < 0.12) {
			op = { op: "withdraw" };
			result = run(() => provider.withdraw(services.s));
		} else if (r < 0.18) {
			op = { op: "replace", shape: pick(shapes) };
			result = run(() => provider.replace(services.s, newImplementation(op.shape)));
		} else if (r < 0.2) {
			op = { op: "validate", shape: pick(shapes) };
			result = run(() => provider.validateReplacement(services.s, newImplementation(op.shape)));
		} else if (r < 0.23) {
			op = { op: "use", service: pick(["s", "s", "k"]) };
			result = run(() => `i${idOf(provider.use(services[op.service]))}`);
		} else if (r < 0.33) {
			op = { op: "spawn", service: step === 1 ? "k" : pick(["k", "k", "k", "k", "s", "x"]), key: pick(["a", "a", "b", "c", ""]), shape: pick(shapes) };
			result = run(() => {
				closers.push(provider.spawn(services[op.service], op.key, newImplementation(op.shape)));
				return `closer${closers.length - 1}`;
			});
		} else if (r < 0.4 && closers.length > 0) {
			op = { op: "close", closer: int(closers.length) };
			result = run(() => closers[op.closer]());
		} else if (r < 0.62 && states.length > 0) {
			const burst = rnd() < 0.2;
			const state = burst && recent.length > 0 ? pick(recent) : rnd() < 0.7 ? Math.max(0, states.length - 1 - int(4)) : int(states.length);
			const count = burst ? 95 + int(40) : 1 + int(2);
			op = { op: "publish", state, frames: [] };
			for (let i = 0; i < count; i++) op.frames.push(publish(state));
			result = "ok";
		} else if (r < 0.74) {
			const index = subscriptions.length;
			op = {
				op: "subscribe",
				service: step === 2 ? "s" : step === 3 ? "k" : pick(["s", "s", "k", "k", "x"]),
				mode: step === 2 ? "singleton" : step === 3 ? "keyed" : pick(["singleton", "keyed"]),
				throws: rnd() < 0.12,
				closeAfter: rnd() < 0.12 ? 1 + int(3) : 0,
			};
			// A reaction runs once from inside the listener, on its at-th update: it publishes a frame to a recent state (recorded when it
			// runs, for the Go replay), opens and activates a nested subscription, withdraws the singleton or disposes the provider.
			if (rnd() < 0.45) {
				const action = pick(["publish", "publish", "publish", "subscribe", "subscribe", "subscribe", "publishSubscribe", "publishSubscribe", "withdraw", "dispose"]);
				op.react = { at: 1 + int(3), action };
				if (action === "subscribe" || action === "publishSubscribe") Object.assign(op.react, { service: pick(["s", "k"]), mode: pick(["singleton", "keyed", "keyed"]) });
			}
			const subscribe = (index, service, mode, spec) => {
				let received = 0;
				const subscription = provider.subscribe(services[service].id, mode, (update) => {
					received += 1;
					events.push(`sub${index} ${JSON.stringify(update)}`);
					if (spec.closeAfter === received) subscriptions[index].close();
					const react = spec.react;
					if (react?.at === received) {
						// publishSubscribe publishes and then subscribes, so a frame queued behind the running delivery reaches a subscriber
						// whose snapshot already holds it.
						if (react.action === "publish" || react.action === "publishSubscribe") {
							react.state = publishing !== undefined && rnd() < 0.7 ? publishing : Math.max(0, states.length - 1 - int(3));
							react.frame = publish(react.state);
						}
						if (react.action === "withdraw") events.push(`sub${index} withdraws ${run(() => provider.withdraw(services.s))}`);
						if (react.action === "dispose") events.push(`sub${index} disposes ${run(() => provider.dispose())}`);
						if (react.action === "subscribe" || react.action === "publishSubscribe") {
							const nested = subscriptions.length;
							subscriptions.push(null);
							events.push(`sub${index} opens ${run(() => subscribe(nested, react.service, react.mode, {}))}`);
						}
					}
					if (spec.throws) throw new Error(`listener ${index} failed on ${update.type}`);
				});
				subscriptions[index] = subscription;
				const snapshot = `sub${index} ${JSON.stringify(subscription.snapshot)}`;
				if (spec !== op) subscription.activate();
				return snapshot;
			};
			subscriptions.push(null);
			result = run(() => subscribe(index, op.service, op.mode, op));
		} else if (r < 0.84 && subscriptions.length > 0) {
			op = { op: "activate", subscription: step === 4 ? 0 : step === 5 ? 1 : int(subscriptions.length) };
			result = subscriptions[op.subscription] === null ? "none" : run(() => subscriptions[op.subscription].activate());
		} else if (r < 0.87 && subscriptions.length > 0) {
			op = { op: "unsubscribe", subscription: int(subscriptions.length) };
			result = subscriptions[op.subscription] === null ? "none" : run(() => subscriptions[op.subscription].close());
		} else if (r < 0.994) {
			const service = pick(["s", "s", "k", "k", "x"]);
			op = { op: "invoke", call: { serviceId: services[service].id, member: pick(["m", "m", "s", "z"]), args: [int(5), "q"].slice(0, int(3)) } };
			if (rnd() < (service === "k" ? 0.9 : 0.1)) op.call.instance = { key: pick(["a", "b", "c"]), generation: 1 + int(3) };
			try {
				result = JSON.stringify(await provider.invoke(op.call, BACKGROUND_CONTEXT));
			} catch (error) {
				result = "!" + describe(error);
			}
		} else {
			op = { op: "dispose" };
			result = run(() => provider.dispose());
		}
		if (["provide", "replace", "spawn"].includes(op.op) && !result.startsWith("!")) recent = created;
		ops.push(op);
		observed.push({ result, events });
	}
	out.push({ ops, observed });
}
process.stdout.write(JSON.stringify(out));
