// Drive the installed Pi chord facet host, never a translated oracle: seeded random sequences of facet reloads over three facets
// (a provides ra; b uses ra and provides rb; c provides rc), where each new generation may throw from setup, change its provisions,
// throw from activation, or throw from an owned cleanup or a deactivation. After each reload the oracle records its result, the
// lifecycle events it ran and which generation answers each service; a final dispose is recorded the same way. The reload specs are
// written out so the Go test replays the same sequence.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { createFacetHost, defineFacet, defineService } = await import(pathToFileURL(root + "dist/index.js"));
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

const services = Object.fromEntries(["ra", "rb", "rc", "rd"].map((name) => [name, defineService(`${prefix}.${name}`)]));
const describe = (error) => {
	if (error instanceof AggregateError) return `agg ${error.message} [${error.errors.map(describe).join("; ")}]`;
	if (typeof error?.code === "string") return `${error.code} ${error.message}`;
	return `- ${error?.message}`;
};
const provides = { a: "ra", b: "rb", c: "rc" };
let events = [];
const spec = (initial) => ({
	setupThrows: !initial && rnd() < 0.06,
	shape: initial || rnd() < 0.9 ? "same" : pick(["none", "extra"]),
	activateThrows: rnd() < (initial ? 0.03 : 0.1),
	ownThrows: rnd() < 0.06,
	deactivateThrows: rnd() < 0.05,
});
const facet = (id, generation, s) =>
	defineFacet({
		id,
		setup(env) {
			const name = `${id}#${generation}`;
			events.push(`setup ${name}`);
			if (id === "b") env.use(services.ra);
			if (s.shape !== "none") env.provide(services[provides[id]], { m: () => name });
			if (s.shape === "extra") env.provide(services.rd, { m: () => name });
			env.own(() => {
				events.push(`own ${name}`);
				if (s.ownThrows) throw new Error(`own ${name} failed`);
			});
			env.onActivate(() => {
				events.push(`activate ${name}`);
				if (s.activateThrows) throw new Error(`activate ${name} failed`);
			});
			env.onDeactivate(() => {
				events.push(`deactivate ${name}`);
				if (s.deactivateThrows) throw new Error(`deactivate ${name} failed`);
			});
			if (s.setupThrows) throw new Error(`setup ${name} failed`);
		},
	});
const settle = async (promise) => {
	try {
		await promise;
		return "ok";
	} catch (error) {
		return "!" + describe(error);
	}
};

const out = [];
for (let scenario = 0; scenario < scenarios; scenario++) {
	events = [];
	const initial = Object.fromEntries(["a", "b", "c"].map((id) => [id, spec(true)]));
	let host;
	const created = await settle(
		(async () => {
			host = await createFacetHost({ facets: ["a", "b", "c"].map((id) => facet(id, 0, initial[id])) });
		})(),
	);
	const probe = async () => {
		const answers = [];
		for (const name of ["ra", "rb", "rc"]) {
			try {
				answers.push(`${name}=${await host.services.invoke({ serviceId: services[name].id, member: "m", args: [] }, BACKGROUND_CONTEXT)}`);
			} catch (error) {
				answers.push(`${name}=!${describe(error)}`);
			}
		}
		return answers;
	};
	const observed = [{ result: created, events, probe: host === undefined ? [] : await probe() }];
	const reloads = [];
	for (let step = 1; host !== undefined && step <= steps; step++) {
		events = [];
		const ids = [];
		for (const id of ["a", "b", "c"]) if (rnd() < 0.45) ids.push(id);
		if (ids.length === 0) ids.push(pick(["a", "b", "c"]));
		if (rnd() < 0.04) ids.push(pick(["a", "z", ""]));
		const reload = ids.map((id) => ({ id, spec: spec(false) }));
		reloads.push(reload);
		const result = await settle(host.reload(reload.map(({ id, spec }) => facet(id, step, spec))));
		observed.push({ result, events, probe: await probe() });
		if (result === "!- Facet host cannot reload while dead") break;
	}
	if (host !== undefined) {
		events = [];
		observed.push({ result: await settle(host.dispose()), events, probe: [] });
	}
	out.push({ initial, reloads, observed });
}
process.stdout.write(JSON.stringify(out));
