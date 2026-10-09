// Drive the installed Pi chord facet host consuming an external service source, never a translated oracle. The source offers a
// singleton s and a keyed k from a live provider through a loopback binding. One facet u uses s and observes k. Seeded random
// sequences mix provider operations (provide, withdraw, replace, spawn, instance close), reloads of u (whose new generation may throw
// from setup or activation) and rebinds of the source's binding. After each step the oracle records the step's result, the host's
// and the observations' reported events, and what every held handle answers: each generation's s handle and each observed k view.
// A final dispose is recorded the same way. The steps are written out so the Go test replays the same sequence.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createFacetHost, createRemoteServiceBinding, defineFacet, defineService } = await import(pathToFileURL(root + "dist/index.js"));
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

const services = Object.fromEntries(["s", "k"].map((name) => [name, defineService(`${prefix}.${name}`)]));
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
const flush = async () => {
	for (let i = 0; i < 3; i++) await new Promise((resolve) => setImmediate(resolve));
};

const out = [];
for (let scenario = 0; scenario < scenarios; scenario++) {
	const provider = new RemoteServiceProvider([
		{ service: services.s, mode: "singleton" },
		{ service: services.k, mode: "keyed" },
	]);
	let events = [];
	let provisions = 0;
	const newImplementation = (service) => {
		const tag = `${service}${provisions++}`;
		return { m: () => tag };
	};
	let binding;
	const source = {
		acceptsUnavailableServices: false,
		catalogue: async () => provider.catalogue,
		open: (options) => {
			binding = createRemoteServiceBinding({ ...options, transport: createLoopbackServiceTransport(provider) });
			return binding;
		},
	};
	const handles = []; // [label, s handle] for each generation of u
	const views = []; // [label, k view] in open order
	const facet = (generation, spec) =>
		defineFacet({
			id: "u",
			setup(env) {
				events.push(`setup u#${generation}`);
				handles.push([`u${generation}.s`, env.use(services.s)]);
				env.observe(services.k, (view) => {
					events.push(`open u#${generation} v${views.length}`);
					views.push([`v${views.length}`, view]);
				});
				env.onActivate(() => {
					// Go starts the first external keyed subscription concurrently with this callback, so generation 0's
					// activation is not ordered against its first observations there; it is not recorded.
					if (generation > 0) events.push(`activate u#${generation}`);
					if (spec.activateThrows) throw new Error(`activate u#${generation} failed`);
				});
				if (spec.setupThrows) throw new Error(`setup u#${generation} failed`);
			},
		});
	const closers = [];
	const ops = [];
	const observed = [];
	const probe = async () => {
		const answers = [];
		for (const [label, handle] of [...handles.slice(-2), ...views]) {
			try {
				answers.push(`${label}=${await handle.m(BACKGROUND_CONTEXT)}`);
			} catch (error) {
				answers.push(`${label}!${describe(error)}`);
			}
		}
		return answers;
	};
	const initial = { provide: rnd() < 0.9, spawn: rnd() < 0.5 };
	if (initial.provide) provider.provide(services.s, newImplementation("s"));
	if (initial.spawn) closers.push(provider.spawn(services.k, "a", newImplementation("k")));
	let host;
	const created = await settle(async () => {
		host = await createFacetHost({
			facets: [facet(0, { setupThrows: false, activateThrows: false })],
			serviceSources: [source],
			onError: (error) => events.push(`error ${describe(error)}`),
		});
	});
	await flush();
	observed.push({ result: created, events, probe: await probe() });
	events = [];
	let generation = 1;
	for (let step = 0; host !== undefined && step < steps; step++) {
		const r = rnd();
		let op;
		let result = "ok";
		if (r < 0.12) {
			op = { op: "provide" };
			result = await settle(() => provider.provide(services.s, newImplementation("s")));
		} else if (r < 0.2) {
			op = { op: "withdraw" };
			result = await settle(() => provider.withdraw(services.s));
		} else if (r < 0.32) {
			op = { op: "replace" };
			result = await settle(() => provider.replace(services.s, newImplementation("s")));
		} else if (r < 0.5) {
			op = { op: "spawn", key: pick(["a", "b", "c"]) };
			result = await settle(() => closers.push(provider.spawn(services.k, op.key, newImplementation("k"))));
		} else if (r < 0.62 && closers.length > 0) {
			op = { op: "close", index: int(closers.length) };
			result = await settle(() => closers[op.index]());
		} else if (r < 0.82) {
			op = { op: "reload", generation, setupThrows: rnd() < 0.1, activateThrows: rnd() < 0.15 };
			result = await settle(() => host.reload([facet(generation, op)]));
			generation++;
		} else if (r < 0.92) {
			op = { op: "rebind", bound: rnd() < 0.6 };
			result = await settle(() => binding.rebind(op.bound, BACKGROUND_CONTEXT));
		} else {
			op = { op: "noop" };
		}
		await flush();
		ops.push(op);
		observed.push({ result, events, probe: await probe() });
		events = [];
		if (result === "!- Facet host cannot reload while dead") break;
	}
	if (host !== undefined) {
		const result = await settle(() => host.dispose());
		await flush();
		// The host disposes the binding it opened from the source.
		const bindingProbe = binding === undefined ? [] : [`binding ${await settle(() => binding.use(services.s))}`];
		observed.push({ result, events, probe: [...(await probe()), ...bindingProbe] });
	}
	out.push({ initial, ops, observed });
}
process.stdout.write(JSON.stringify(out));
