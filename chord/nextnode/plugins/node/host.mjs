// The Node subprocess of chord/nextnode/plugins (docs/plan/chord-plugins-node-evaluator.md): one chord host whose plugins
// are shipped scripts, evaluated and loaded by Pi's own plugin runtime, linked to the Go host over one framed socket.
//
//   node host.mjs <socket address>
//
// Frames (4-byte big-endian length, one kind byte, the payload): kind 0 is one chord link message (Pi's `ipcLink` carries
// it, so the Go host and this one speak the pinned link wire); kind 1 is one control message, a JSON object. Control
// requests are `{ id, op, ... }` and are answered by one `{ id, ok: true, result }` or `{ id, ok: false, error }`; `abort`
// and `close` have no answer (a closed host ends this process's channel, and the process exits). Unrequested:
// `{ event: "started" }`, `{ event: "activity", counts }` (the host's keep-alive counts), `{ event: "error", error }` (the host's onError) and `{ event: "fatal", error }` (an uncaught
// exception; the process exits).
//
// The host is the child of the Go side: `load` makes it, and it `connect`s the link as its parent link with the principal
// `peer` (trusted), as Pi's forked worker does. The Go side attaches the other end (`NodeHost.Link`) to its host, with a
// route of the host's kind or `accept`. A link silent for the heartbeat (30 s) is closed by this host, and the process
// exits with it.
import { EventEmitter } from "node:events";
import { registerHooks } from "node:module";
import { connect } from "node:net";
import { pathToFileURL } from "node:url";
import { openHostStore } from "./store.mjs";

const tree = new URL("./pi/chord/src/", import.meta.url);
const pi = (path) => import(new URL(path, tree).href);
// The app's modules (core facets, host packages) import Pi's chord by its package name, as they do in Pi's own runtime: the
// names are answered with the copies this process runs, so their instances are the host's.
const CHORD = { "@earendil-works/chord/next": "next/index.ts", "@earendil-works/chord/context": "context/index.ts" };
registerHooks({
	resolve(specifier, context, nextResolve) {
		const file = CHORD[specifier];
		return file === undefined ? nextResolve(specifier, context) : { url: new URL(file, tree).href, shortCircuit: true };
	},
});
const [chordNext, { ipcLink }, { createPluginRuntime }, { mapStack }, { BACKGROUND_CONTEXT, withAbortSignal }, { nodeCompression }] =
	await Promise.all([
		pi("next/index.ts"),
		pi("next/link/index.ts"),
		pi("next-node/plugins/runtime.ts"),
		pi("next-node/plugins/stack.ts"),
		pi("context/index.ts"),
		pi("next-node/compression.ts"),
	]);
const { createHost, isChordError } = chordNext;

const KIND_LINK = 0;
const KIND_CONTROL = 1;

/** A framed socket as the IPC channel `ipcLink` takes: Uint8Array messages are the link's, anything else the app's. */
class FramedPort extends EventEmitter {
	#socket;
	#buffer = Buffer.alloc(0);
	connected = true;

	constructor(socket) {
		super();
		this.#socket = socket;
		socket.on("data", (chunk) => this.#read(chunk));
		const ended = () => {
			if (!this.connected) return;
			this.connected = false;
			this.emit("disconnect");
		};
		socket.on("close", ended);
		socket.on("error", ended);
	}

	#read(chunk) {
		this.#buffer = this.#buffer.length === 0 ? chunk : Buffer.concat([this.#buffer, chunk]);
		for (;;) {
			if (this.#buffer.length < 4) return;
			const length = this.#buffer.readUInt32BE(0);
			if (this.#buffer.length < 4 + length) return;
			const kind = this.#buffer[4];
			const payload = this.#buffer.subarray(5, 4 + length);
			this.#buffer = this.#buffer.subarray(4 + length);
			if (kind === KIND_LINK) this.emit("message", new Uint8Array(payload));
			else if (kind === KIND_CONTROL) this.emit("message", JSON.parse(payload.toString("utf8")));
		}
	}

	#write(kind, payload, callback) {
		const header = Buffer.allocUnsafe(5);
		header.writeUInt32BE(1 + payload.length, 0);
		header[4] = kind;
		this.#socket.write(header);
		return this.#socket.write(payload, (error) => callback(error ?? null));
	}

	send(message, callback) {
		return this.#write(KIND_LINK, message, callback);
	}

	control(message, callback = () => {}) {
		return this.#write(KIND_CONTROL, Buffer.from(JSON.stringify(message)), callback);
	}

	disconnect() {
		this.connected = false;
		this.#socket.end();
	}
}

/**
 * An error as it crosses to the Go side: its message, its stack mapped through the plugin scripts' source maps, and its
 * `cause` chain (a cause that is no Error is an error of its string; a cause already on the chain ends it, so a cycle does).
 */
function wireError(error, seen = new Set()) {
	seen.add(error);
	const stack = mapStack(error);
	const cause = error?.cause;
	const wired =
		cause !== undefined && !seen.has(cause) ? { cause: wireError(cause, seen) } : {};
	if (isChordError(error)) return { name: "ChordError", message: error.message, stack, chord: error.toJSON(), ...wired };
	if (error instanceof Error) return { name: error.name, message: error.message, stack, ...wired };
	return { name: "Error", message: String(error), stack, ...wired };
}

function wireCause(cause) {
	return { kind: cause.kind, error: cause.error.toJSON() };
}

function wireObject(object) {
	return object.kind === "parked" ? { ...object, cause: wireCause(object.cause) } : object;
}

const socket = connect(process.argv[2]);
await new Promise((resolve, reject) => {
	socket.once("connect", resolve);
	socket.once("error", reject);
});
const port = new FramedPort(socket);
// Made at once: the messages it receives queue until the host that connects it listens.
const link = ipcLink(port);

/** The one host and runtime of this process, set by `load`. */
let hosted;
/** The abort controllers of the running requests, by id. */
const running = new Map();

port.on("disconnect", () => {
	// The Go side is gone: nothing is left to report to.
	process.exit(0);
});

process.on("uncaughtException", (error) => {
	port.control({ event: "fatal", error: wireError(error) }, () => process.exit(1));
});

async function load(request, context) {
	if (hosted !== undefined) throw new Error("load: this process loaded its host already");
	const modules = { "@earendil-works/chord/next": chordNext };
	for (const [specifier, file] of Object.entries(request.modules)) modules[specifier] = await import(pathToFileURL(file).href);
	const core = [];
	for (const { name, module } of request.core) {
		core.push({ name, facet: (await import(pathToFileURL(module).href)).default });
	}
	const store = request.store === undefined ? undefined : await openHostStore(request.store.path, context);
	const host = createHost({
		...(store === undefined ? {} : { store }),
		...(request.address === undefined ? {} : { address: request.address }),
		...(request.principal === undefined ? {} : { principal: request.principal }),
		...(request.heartbeat === undefined ? {} : { heartbeat: request.heartbeat }),
		...(request.closeTimeout === undefined ? {} : { closeTimeout: request.closeTimeout }),
		...(request.readyTimeout === undefined ? {} : { readyTimeout: request.readyTimeout }),
		...(request.compression === true ? { compression: nodeCompression } : {}),
		onError: (error) => port.control({ event: "error", error: hostError(error) }),
	});
	const runtime = createPluginRuntime();
	hosted = { host, runtime, store };
	// The host's keep-alive counts, for the app that supervises it (NodeOptions.OnActivity): now, and at each change.
	port.control({ event: "activity", counts: host.activity.value });
	void host.activity.watch(BACKGROUND_CONTEXT).then((watch) =>
		watch.start(async (call) => {
			if (call.kind === "value") port.control({ event: "activity", counts: call.value });
		}),
	);
	// As a worker connects to its parent (next-node/workers/main.ts): the Go side attaches the other end of the link, as the
	// server of a worker does, and `peer` is who the Go side is, as this host stamps it.
	host.connect(link, { principal: request.peer, trusted: true }, context);
	const report = await runtime.load(host, { kind: request.kind, core, scripts: request.scripts, modules }, context);
	return { failed: report.failed.map(({ plugin, error }) => ({ plugin, error: wireError(error) })) };
}

/**
 * What the host reports in the background, as data: every field of its kind (host.ts's `HostError`, the batch's and the
 * registries'), its errors as wire errors and its failure as its kind and ChordError.
 */
function hostError(error) {
	const wired = {};
	for (const [key, value] of Object.entries(error)) {
		if (key === "error") wired.error = wireError(value);
		else if (key === "failure") wired.failure = wireCause(value);
		else wired[key] = value;
	}
	return wired;
}

async function reload(request, context) {
	if (hosted === undefined) throw new Error("reload: no host is loaded");
	const report = await hosted.runtime.reload(hosted.host, request.scripts, context);
	return {
		plugins: report.plugins,
		failed: report.failed.map(({ plugin, error }) => ({ plugin, error: wireError(error) })),
		objects: report.objects.map(wireObject),
	};
}

const operations = { load, reload };

port.on("message", (message) => {
	if (message instanceof Uint8Array) return;
	if (message.op === "abort") {
		running.get(message.target)?.abort(new Error("the request was aborted by the Go side"));
		return;
	}
	if (message.op === "close") {
		// The host's close ends its links, this process's channel with them; the app's store is closed after the host, as
		// Pi's apps do (a host that failed to close still lets the store close); the process ends once both are closed.
		(hosted?.host.close(BACKGROUND_CONTEXT) ?? Promise.resolve())
			.finally(() => hosted?.store?.close(BACKGROUND_CONTEXT))
			.finally(() => process.exit(0));
		return;
	}
	const operation = operations[message.op];
	const controller = new AbortController();
	running.set(message.id, controller);
	const context = withAbortSignal(controller.signal, BACKGROUND_CONTEXT);
	const answer = operation === undefined ? Promise.reject(new Error(`unknown control request ${message.op}`)) : operation(message, context);
	answer.then(
		(result) => ({ id: message.id, ok: true, result }),
		(error) => ({ id: message.id, ok: false, error: wireError(error) }),
	).then((reply) => {
		running.delete(message.id);
		port.control(reply);
	});
});

port.control({ event: "started" });
