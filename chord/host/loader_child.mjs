// The child process of the isolated Node facet loader (chord/host/loader.go). It plays packages/chord/src/loader.ts's
// nodeLoader inside a process of its own: it imports the facet modules (default export: a facet), installs them on a
// Chord host, and answers the parent's requests to open, call and release the objects those facets serve.
//
// Control protocol: the manager's control channel ({ chord2control }: init, ready, fatal, shutdown) carries the start
// request (the first init) and, as further init messages, the parent's requests ({ chord2loader }). Replies and the
// facet table go back as plain IPC messages ({ chord2loader }).
import { existsSync, readFileSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const send = (message) => {
	if (process.connected) process.send(message);
};
const control = (message) => send({ chord2control: message });
const loader = (message) => send({ chord2loader: message });

const fail = (error) => {
	const name = error instanceof Error ? error.name : "Error";
	const message = error instanceof Error ? error.message : String(error);
	loader({ type: "failed", name, message });
	control({ type: "fatal", message });
	setTimeout(() => process.exit(1), 200);
};

// The parent going away ends the child; nothing else owns it.
process.on("disconnect", () => process.exit(0));

// loader.ts toUrl: a module is a file URL or a path.
const toUrl = (module) => {
	const url = module.startsWith("file:") ? new URL(module) : pathToFileURL(resolve(module));
	url.search = "";
	return url;
};

// The chord package's entry: CHORD_LOADER_CHORD (a file, a URL or a package directory), else the
// @earendil-works/chord install found above the first module.
const chordEntry = (first) => {
	const spec = process.env.CHORD_LOADER_CHORD ?? "";
	const fromPackage = (directory) => {
		const manifest = JSON.parse(readFileSync(join(directory, "package.json"), "utf8"));
		const entry = manifest.exports?.["."];
		const target = typeof entry === "string" ? entry : (entry?.import ?? entry?.default);
		if (typeof target !== "string") throw new TypeError(`${directory}: package.json exports no "." import`);
		return pathToFileURL(join(directory, target)).href;
	};
	if (spec !== "") {
		if (spec.startsWith("file:")) return spec;
		const path = resolve(spec);
		return statSync(path).isDirectory() ? fromPackage(path) : pathToFileURL(path).href;
	}
	const firstPath = fileURLToPath(first);
	for (let directory = dirname(firstPath); ; directory = dirname(directory)) {
		const candidate = join(directory, "node_modules", "@earendil-works", "chord");
		if (existsSync(join(candidate, "package.json"))) return fromPackage(candidate);
		if (dirname(directory) === directory) break;
	}
	throw new TypeError(`@earendil-works/chord is not installed above ${firstPath}`);
};

const unsupported = (message) => new TypeError(`isolated Node facet loader: ${message}`);

// What the parent mirrors of a served contract: methods only (state, opaque members and several variants need the
// served object itself on the parent's host).
const checkServed = (facet, type, options) => {
	const { description } = type;
	const variants = Object.keys(description.variants);
	if (description.scope === "remote" && variants.length !== 1) {
		// pig divergence (D178): several variants are refused.
		throw unsupported(`${facet}: ${type.name} has ${variants.length} variants; the parent mirrors one`);
	}
	for (const members of Object.values(description.variants)) {
		for (const [name, member] of Object.entries(members)) {
			if (member.kind !== "method") {
				// pig divergence (D178): state and opaque members are refused.
				throw unsupported(`${facet}: ${type.name}.${name} is a ${member.kind} member; the parent mirrors methods`);
			}
		}
	}
	// pig divergence (D178): accepts adapters are refused.
	if (options.accepts !== undefined) throw unsupported(`${facet}: ${type.name} serves with accepts adapters`);
};

// Whether the served method takes no arguments (it is called as `ref.name(ctx)`).
const takesNoArgs = (type, member) => {
	for (const members of Object.values(type.description.variants)) {
		const description = members[member];
		if (description?.kind === "method") return description.args === undefined;
	}
	return false;
};

let host;
const served = new Map();
const handles = new Map();
const types = new Map();
const calls = new Map();

const start = async ({ modules }) => {
	const urls = modules.map(toUrl);
	const { createHost } = await import(chordEntry(urls[0]));
	// Pi's createHost needs `self`, the principal of the host's own code; the child acts as the Go host's own principal.
	host = createHost({
		name: "node-facets",
		self: { host: true },
		onError: (error) => process.stderr.write(`${error?.stack ?? error}\n`),
	});
	const facets = [];
	const table = [];
	for (const url of urls) {
		const name = url.href;
		const loaded = await import(name);
		const definition = loaded.default;
		if (definition === undefined || typeof definition.setup !== "function") {
			throw new TypeError(`${name}: default export must be a facet (defineFacet)`);
		}
		const records = [];
		facets.push({
			name,
			uses: definition.uses,
			setup: (api) =>
				definition.setup({
					...api,
					serve: (type, factory, options = {}) => {
						checkServed(name, type, options);
						records.push({ type, options });
						served.set(type.name, type);
						return api.serve(type, factory, options);
					},
				}),
		});
		table.push({ name, uses: definition.uses ?? [], records });
	}
	await host.load(facets);
	loader({
		type: "facets",
		facets: table.map(({ name, uses, records }) => ({
			name,
			uses: uses.map((type) => type.description),
			serves: records.map(({ type, options }) => ({
				description: type.description,
				...(options.linger === undefined ? {} : { linger: options.linger }),
				...(options.lifetime === undefined ? {} : { lifetime: options.lifetime }),
			})),
		})),
	});
};

const errorOf = (error) => ({
	name: error instanceof Error ? error.name : "Error",
	message: error instanceof Error ? error.message : String(error),
	...(typeof error?.code === "string" && error?.name === "ChordError" ? { code: error.code } : {}),
	...(typeof error?.reason === "string" ? { reason: error.reason } : {}),
});

const reply = (id, run) =>
	Promise.resolve()
		.then(run)
		.then(
			(value) => loader({ type: "reply", id, ok: true, ...(value === undefined ? {} : { value }) }),
			(error) => loader({ type: "reply", id, ok: false, error: errorOf(error) }),
		);

const request = (message) => {
	const { id } = message;
	switch (message.type) {
		case "open":
			return reply(id, async () => {
				const type = served.get(message.contract);
				if (type === undefined) throw new TypeError(`${message.contract} is not served`);
				// `hold` is Pi's synchronous reference (`get` resolves once the reference is ready).
				const handle = host.hold(type, message.input);
				handles.set(message.handle, handle);
				types.set(message.handle, type);
				try {
					await handle.ready;
				} catch (error) {
					// A failed open is never closed by the parent: release here.
					handles.delete(message.handle);
					types.delete(message.handle);
					handle.release();
					throw error;
				}
			});
		case "call":
			return reply(id, async () => {
				const handle = handles.get(message.handle);
				if (handle === undefined) throw new TypeError(`handle ${message.handle} is not open`);
				const abort = new AbortController();
				calls.set(id, abort);
				try {
					// A method without arguments is called as `ref.name(ctx)`.
					const noArgs = takesNoArgs(types.get(message.handle), message.member);
					return await host.root((ctx) => (noArgs ? handle[message.member](ctx) : handle[message.member](message.args, ctx)), {
						signal: abort.signal,
					});
				} finally {
					calls.delete(id);
				}
			});
		case "cancel":
			calls.get(message.target)?.abort();
			return undefined;
		case "release":
			return reply(id, () => {
				handles.get(message.handle)?.release();
				handles.delete(message.handle);
				types.delete(message.handle);
			});
		default:
			return undefined;
	}
};

let started = false;
process.on("message", (message) => {
	const frame = message?.chord2control;
	if (frame?.type === "shutdown") {
		Promise.resolve(host?.close()).finally(() => process.exit(0));
		return;
	}
	if (frame?.type !== "init") return;
	if (!started) {
		started = true;
		start(frame.value).then(() => control({ type: "ready" }), fail);
		return;
	}
	const inner = frame.value?.chord2loader;
	if (inner !== undefined) void request(inner);
});
