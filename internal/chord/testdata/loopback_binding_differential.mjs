// Drive the installed Pi chord binding over a loopback transport to a live provider, never a translated oracle: seeded random
// sequences of consumer operations (use, observe, unobserve, rebind, dispose) and provider operations (provide, withdraw, replace,
// spawn, instance close, state change). After each step the oracle waits for binding readiness, then records the step's result, the
// readiness result, the binding's reported errors, the observations it opened and what every held handle answers: each method call's
// result and each state member's value. The steps are written out so the Go test replays the same sequence.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createRemoteServiceBinding, defineService, replicatedState } = await import(pathToFileURL(root + "dist/index.js"));
const { RemoteServiceProvider } = await import(pathToFileURL(root + "dist/services/provider.js"));
const { createLoopbackServiceTransport } = await import(pathToFileURL(root + "dist/services/loopback.js"));
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

const services = Object.fromEntries(["s", "k", "x"].map((name) => [name, defineService(`${prefix}.${name}`)]));
const describe = (error) => {
	if (error instanceof AggregateError) return `agg ${error.message} [${error.errors.map(describe).join("; ")}]`;
	if (typeof error?.code === "string") return `${error.code} ${error.message}`;
	return `- ${error?.message}`;
};
const settle = async (fn) => {
	try {
		await fn();
		return "ok";
	} catch (error) {
		return "!" + describe(error);
	}
};
const flush = () => new Promise((resolve) => setImmediate(resolve));

const out = [];
for (let scenario = 0; scenario < scenarios; scenario++) {
	const provider = new RemoteServiceProvider([
		{ service: services.s, mode: "singleton" },
		{ service: services.k, mode: "keyed" },
	]);
	let events = [];
	const binding = createRemoteServiceBinding({
		services: [services.s, services.k],
		transport: createLoopbackServiceTransport(provider),
		onError: (error) => events.push(`error ${describe(error)}`),
	});
	let provisions = 0;
	const states = []; // the state of every provision, by provision number
	const newImplementation = (service) => {
		const tag = `${service}${provisions++}`;
		const st = replicatedState({ n: 0 });
		states.push(st);
		return { m: () => tag, st };
	};
	const closers = [];
	const handles = []; // [service, handle] in first-use order
	const views = []; // observed keyed views in open order
	const unobservers = [];
	const ops = [];
	const observed = [];
	for (let step = 0; step < steps; step++) {
		events = [];
		const r = rnd();
		let op;
		let result = "ok";
		if (r < 0.12) {
			op = { op: "use", service: step === 0 ? "s" : pick(["s", "s", "k", "x"]) };
			result = await settle(() => {
				const handle = binding.use(services[op.service]);
				if (!handles.some(([, held]) => held === handle)) handles.push([op.service, handle]);
			});
		} else if (r < 0.24) {
			op = { op: "observe", service: step === 1 ? "k" : pick(["k", "k", "s", "x"]) };
			const index = unobservers.length;
			result = await settle(() => {
				unobservers.push(
					binding.observe(services[op.service], (view) => {
						events.push(`open ${index} ${views.length}`);
						views.push(view);
					}),
				);
			});
		} else if (r < 0.29 && unobservers.length > 0) {
			op = { op: "unobserve", index: int(unobservers.length) };
			unobservers[op.index]();
		} else if (r < 0.37) {
			op = { op: "rebind", bound: rnd() < 0.5 };
			result = await settle(() => binding.rebind(op.bound, BACKGROUND_CONTEXT));
		} else if (r < 0.39 && step >= steps - 6) {
			op = { op: "dispose" };
			result = await settle(() => binding.dispose(BACKGROUND_CONTEXT));
		} else if (r < 0.48) {
			op = { op: "provide" };
			result = await settle(() => provider.provide(services.s, newImplementation("s")));
		} else if (r < 0.53) {
			op = { op: "withdraw" };
			result = await settle(() => provider.withdraw(services.s));
		} else if (r < 0.61) {
			op = { op: "replace" };
			result = await settle(() => provider.replace(services.s, newImplementation("s")));
		} else if (r < 0.74) {
			op = { op: "spawn", key: pick(["a", "b"]) };
			result = await settle(() => closers.push(provider.spawn(services.k, op.key, newImplementation("k"))));
		} else if (r < 0.82 && closers.length > 0) {
			op = { op: "close", index: int(closers.length) };
			result = await settle(() => closers[op.index]());
		} else if (states.length > 0) {
			op = { op: "change", index: int(states.length), n: step };
			result = await settle(() => states[op.index].change(BACKGROUND_CONTEXT, (draft) => void (draft.n = op.n)));
		} else {
			op = { op: "noop" };
		}
		ops.push(op);
		const ready = await settle(() => binding.ready(BACKGROUND_CONTEXT));
		await flush();
		const probe = [];
		for (const [label, handle] of [...handles, ...views.map((view, index) => [`v${index}`, view])]) {
			try {
				probe.push(`${label}.m=${await handle.m(BACKGROUND_CONTEXT)}`);
			} catch (error) {
				probe.push(`${label}.m!${describe(error)}`);
			}
			try {
				probe.push(`${label}.st=${JSON.stringify(handle.st.value) ?? "undefined"}`);
			} catch (error) {
				probe.push(`${label}.st!${describe(error)}`);
			}
		}
		await flush();
		observed.push({ result, ready, events, probe });
	}
	await binding.dispose(BACKGROUND_CONTEXT).catch(() => {});
	out.push({ ops, observed });
}
process.stdout.write(JSON.stringify(out));
