// The control requests of `TestPlugin` (plugins_testing.go), the Go form of Pi's testing.ts (`testPlugin`, slice 12 §6): what
// the helper reads of its host's own internals (`undeclaredRegistries`, `activationsUnderWay`, `timeouts.ready`,
// `unservedNames`) and the collecting facet it adds by a reload. testing.ts runs in the process of the host it tests; here the
// host is in this subprocess, so the helper's functions run here, and the Go side asks for their results.
//
//   testCollect       the collection of testing.ts: the registries with contributions and no provider are served by a
//                     collecting facet, until no new one appears. Answers the names collected.
//   testContributions the table of one collected registry as data (a closure in a contribution does not cross).
//   testUnserved      the names requests wait for unbound that nobody serves (`unservedNames`).
//   testRoot          `host.root` run as the host's principal or `principal`, answering the principal its context has.
import { encodeValue } from "./wire.mjs";

/** The plugin name of the collecting facet, beside the plugin and its stubs (testing.ts's `COLLECTOR`). */
const COLLECTOR = "chord.test-plugin-collector";

/**
 * Every older version's contributions and readers as they are: made per version looked up, not per version below (testing.ts's
 * `olderAs`; the kernel only looks adapters up by version, `accepts[version]`).
 */
function olderAs(token) {
	const asItIs = { contribution: (old) => old, value: (table) => table };
	return new Proxy(
		{},
		{
			get: (_target, key) => (typeof key === "string" && /^\d+$/.test(key) && Number(key) < token.version ? asItIs : undefined),
		},
	);
}

/**
 * Waits until no activation on `host` is under way (as `load`'s barrier waits for first outcomes), bounded by the host's
 * `readyTimeout`: the objects a reload's publication woke reach their outcome, slow openers included.
 */
async function settle(host) {
	const deadline = Date.now() + host.timeouts.ready;
	// The woken requests check again in promise jobs and I/O callbacks: let those run before counting.
	do {
		for (let turn = 0; turn < 3; turn++) await new Promise((resolve) => setImmediate(resolve));
		if (host.activationsUnderWay() === 0) return;
		await new Promise((resolve) => setTimeout(resolve, 5));
	} while (Date.now() < deadline);
}

/** Throws the failures of a load or reload: a test of a plugin whose setup (or a stub's) failed has nothing to test. */
function throwFailed(report) {
	const [first] = report.failed;
	if (first === undefined) return;
	const message = report.failed.map(({ error }) => (error instanceof Error ? error.message : String(error))).join("; ");
	throw new Error(`testPlugin: setup failed: ${message}`, { cause: first.error });
}

/** The control requests, as `host.mjs` serves them: `hosted()` is the process's loaded host (undefined before `load`). */
export function testOperations({ hosted, chord }) {
	const { defineFacet, defineRegistry } = chord;
	function loaded(op) {
		const state = hosted();
		if (state === undefined) throw new Error(`${op}: no host is loaded`);
		return state;
	}

	async function testCollect(_request, context) {
		const state = loaded("testCollect");
		const { host, runtime, core } = state;
		const tokens = new Map();
		// A fixed point: an object that goes live once a collected registry is served may contribute to another.
		for (;;) {
			await settle(host);
			const fresh = host.undeclaredRegistries().filter(({ name }) => !tokens.has(name));
			if (fresh.length === 0) break;
			for (const { name, version } of fresh) tokens.set(name, defineRegistry(name, { version }));
			const served = [...tokens.values()];
			const collector = defineFacet((facetHost) => {
				for (const token of served) facetHost.serve(token, { compose: (table) => table, accepts: olderAs(token) });
			});
			// The runtime reloads with its core facets and the scripts of its load: the collector is one more core facet.
			const at = core.findIndex(({ name }) => name === COLLECTOR);
			if (at >= 0) core.splice(at, 1);
			core.push({ name: COLLECTOR, facet: collector });
			throwFailed(await runtime.reload(host, state.scripts, context));
		}
		state.collected = tokens;
		return { names: [...tokens.keys()] };
	}

	async function testContributions(request, context) {
		const state = loaded("testContributions");
		const token = state.collected?.get(request.name);
		if (token === undefined) {
			throw new Error(`testPlugin: registry ${request.name} is not collected (nothing contributed to it unserved at load)`);
		}
		const { value } = await state.host.registry(token, context);
		// The collecting provider composes to its table.
		return {
			table: value.map((entry) => ({
				plugin: entry.plugin,
				...(entry.type === undefined ? {} : { type: entry.type }),
				version: entry.version,
				value: encodeValue(entry.value),
			})),
		};
	}

	async function testUnserved() {
		return { names: loaded("testUnserved").host.unservedNames() };
	}

	async function testRoot(request, context) {
		const options = request.principal === undefined ? undefined : { principal: request.principal };
		const principal = await loaded("testRoot").host.root(async (ctx) => ctx.principal, context, options);
		return principal === undefined ? {} : { principal };
	}

	return { testCollect, testContributions, testUnserved, testRoot };
}
