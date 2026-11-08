// The Node subprocess of chord/nextnode/plugins (docs/plan/chord-plugins-node-evaluator.md): one chord host whose plugins
// are shipped scripts, evaluated and loaded by Pi's own plugin runtime, linked to the Go host over one framed socket.
//
//   node host.mjs <socket address>
//
// Frames (4-byte big-endian length, one kind byte, the payload): kind 0 is one chord link message (Pi's `ipcLink` carries
// it, so the Go host and this one speak the pinned link wire); kind 1 is one control message, a JSON object. Control
// requests are `{ id, op, ... }` and are answered by one `{ id, ok: true, result }` or `{ id, ok: false, error }`; `attach`,
// `abort` and `close` have no answer (a closed host ends this process's channel, and the process exits). Unrequested:
// `{ event: "started" }`, `{ event: "closing" }` (the host began the close a `close` asked for), `{ event: "activity", counts }` (the host's keep-alive counts), `{ event: "error", error }` (the host's onError) and `{ event: "fatal", error }` (an uncaught
// exception or rejection; the process exits with code 1, after the exception was printed on stderr as Node prints it).
// The notifications of bridge.mjs (`unwatch`, `release`) have no answer either; its events are `frame`, `watchEnd` and `state`, and it serves the
// requests that hold a local type of the plugins for the Go side (`get`, `value`, `watch`, `call`) and contracts as data (`describe`).
// The calls to the Go side are `{ event: "call", call, name, args }` (answered by the request `{ op: "answer", id: call, ok,
// result | error }`) and `{ event: "cancel", call }` (callbacks.mjs), and the telemetry the host reports is
// `{ event: "telemetry", op, ... }` (telemetry.mjs). The host's synchronous callbacks (`principalKey`, `revokedBy`) use a second
// connection owned by a worker thread (callbacks.mjs).
//
// The host is the child of the Go side: `load` makes it. It `connect`s the link as its parent link with the principal `peer`
// (trusted), as Pi's forked worker does, when the Go side first uses the other end (`NodeHost.Link`, which the Go side
// attaches to its host with a route of the host's kind or `accept`): the control request `attach`, which has no answer. A
// link silent for the heartbeat (30 s) is closed by this host, and the process exits with it; the heartbeat starts with the
// link, so a host nothing attaches has none.
import { EventEmitter } from "node:events";
import { enableCompileCache, flushCompileCache } from "node:module";
import { connect } from "node:net";
import { fileURLToPath, pathToFileURL } from "node:url";
import "./resolve.mjs";
import * as callbacks from "./callbacks.mjs";
import { importGoPackage } from "./gopackage.mjs";
import { openHostStore } from "./store.mjs";
import { createTelemetry } from "./telemetry.mjs";
import { inspect } from "node:util";

// V8's code cache for the modules below (Pi's vendored closure, type-stripped on import), kept in the extracted tree: a
// process after the first starts from compiled code. Node writes the cache of the modules a process compiled when the process
// exits, one file after another, which on Windows took seconds for a cold cache (a new tree, new plugins) and outlasted the
// Go side's stop grace when the link closed: the process was killed after it had ended its channel and run its exit handlers.
// So the cache is written once the process is up and after each load and reload (`flushCompileCache`), where no grace runs,
// and the exit has left to write only what was compiled after them (a dynamic import in a plugin's call).
enableCompileCache(fileURLToPath(new URL("./v8/", import.meta.url)));

const tree = new URL("./pi/chord/src/", import.meta.url);
const pi = (path) => import(new URL(path, tree).href);
const [chordNext, { ipcLink }, { createPluginRuntime, PluginError }, { mapStack }, { BACKGROUND_CONTEXT, withAbortSignal, withTelemetry }, { nodeCompression }, { canonicalJson }] =
	await Promise.all([
		pi("index.ts"),
		pi("link/index.ts"),
		pi("node/plugins/runtime.ts"),
		pi("node/plugins/stack.ts"),
		pi("context/index.ts"),
		pi("node/compression.ts"),
		pi("data/internal.ts"),
	]);
const { createHost, isChordError } = chordNext;

const KIND_LINK = 0;
const KIND_CONTROL = 1;

/** A framed socket as the IPC channel `ipcLink` takes: Uint8Array messages are the link's, anything else the app's. */
class FramedPort extends EventEmitter {
	#socket;
	#buffer = Buffer.alloc(0);
	/** The frames whose write has not completed, and whether `disconnect` waits for them to end the socket. */
	#writing = 0;
	#ending = false;
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
		this.#writing++;
		return this.#socket.write(payload, (error) => {
			this.#writing--;
			if (this.#ending && this.#writing === 0) this.#socket.end();
			callback(error ?? null);
		});
	}

	send(message, callback) {
		return this.#write(KIND_LINK, message, callback);
	}

	control(message, callback = () => {}) {
		return this.#write(KIND_CONTROL, Buffer.from(JSON.stringify(message)), callback);
	}

	/** Ends the writing side once every frame written is out: an `end()` while a write is in flight loses it (Windows). */
	disconnect() {
		this.connected = false;
		this.#ending = true;
		if (this.#writing === 0) this.#socket.end();
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
// The calls of this process to the Go side (GoPackage functions, the host's principalKey and revokedBy) use this channel.
callbacks.attach(port);
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

/**
 * What Node prints on stderr for an uncaught exception: the error as `util.inspect` shows it (its stack, own properties and
 * `[cause]`), a blank line and the Node version; a thrown value that is no Error is its text, with the hint Node gives for a
 * primitive. The positions in plugin scripts are mapped through their source maps (`mapStack`), as the app's own reporters
 * map them. The excerpt of the source line Node prints above the error is not printed.
 */
function uncaughtText(error) {
	let body;
	if (error instanceof Error) body = mapStack(Object.assign(new Error(), { stack: inspect(error) }));
	else {
		body = typeof error === "string" ? error : inspect(error);
		if (error === null || (typeof error !== "object" && typeof error !== "function")) {
			body += "\n(Use `node --trace-uncaught ...` to show where the exception was thrown)";
		}
	}
	return `${body}\n\nNode.js ${process.version}\n`;
}

// Node's own handling of an uncaught exception, which this handler replaces: it prints the error, and the process ends with
// code 1. The `fatal` follows the print, so the app has the text when it has the event. An unhandled rejection is raised as an
// uncaught exception (Node's default `--unhandled-rejections=throw`), so it comes here too.
process.on("uncaughtException", (error) => {
	const fatal = () => port.control({ event: "fatal", error: wireError(error) }, () => process.exit(1));
	try {
		process.stderr.write(uncaughtText(error), fatal);
	} catch {
		fatal();
	}
});

async function load(request, context) {
	if (hosted !== undefined) throw new Error("load: this process loaded its host already");
	const modules = { "@earendil-works/chord": chordNext };
	for (const [specifier, file] of Object.entries(request.modules)) modules[specifier] = await import(pathToFileURL(file).href);
	for (const pkg of request.goPackages) modules[pkg.specifier] = await importGoPackage(pkg);
	// principalKey and revokedBy are synchronous in the host: they call the Go side on a connection of their own.
	if (request.syncToken !== undefined) await callbacks.startSync(process.argv[2], request.syncToken);
	const telemetry = request.telemetry === undefined ? undefined : createTelemetry({ emit: (event) => port.control({ event: "telemetry", ...event }), ...request.telemetry });
	// The proxies of the Go host's own facets import it (bridge.mjs).
	modules[GO_PACKAGE] = bridge.hostPackage;
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
		...(request.reloadDrainTimeout === undefined ? {} : { reloadDrainTimeout: request.reloadDrainTimeout }),
		...(request.compression === true ? { compression: nodeCompression } : {}),
		...(telemetry === undefined ? {} : { telemetry }),
		...(request.principalKey === true
			? { principalKey: callbacks.memoizedByPrincipal((principal) => canonicalJson(principal, BACKGROUND_CONTEXT), (principal) => callbacks.callSync("principalKey", principal)) }
			: {}),
		...(request.revokedBy === true ? { revokedBy: (principal) => callbacks.callSync("revokedBy", principal) } : {}),
		onError: (error) => port.control({ event: "error", error: hostError(error) }),
	});
	const runtime = createPluginRuntime();
	// The names the runtime offered the host (`TestPlugin` tells a plugin that failed before the host, in its build or
	// evaluation, from one whose setup failed in it: a plugin that never reached the host was not offered).
	const offered = [];
	const hostLoad = host.load.bind(host);
	host.load = (facets, loadContext) => {
		offered.push(...facets.map(({ name }) => name));
		return hostLoad(facets, loadContext);
	};
	// What the host's work runs under: the app's telemetry parent, when it gave an adapter.
	const base = telemetry === undefined ? BACKGROUND_CONTEXT : withTelemetry(telemetry, BACKGROUND_CONTEXT);
	hosted = { host, runtime, store, peer: request.peer, base, core, scripts: request.scripts, offered };
	// The host's keep-alive counts, for the app that supervises it (NodeOptions.OnActivity): now, and at each change.
	port.control({ event: "activity", counts: host.activity.value });
	void host.activity.watch(BACKGROUND_CONTEXT).then((watch) =>
		watch.start(async (call) => {
			if (call.kind === "value") port.control({ event: "activity", counts: call.value });
		}),
	);
	const traced = telemetry === undefined ? context : withTelemetry(telemetry, context);
	const wireFailures = (failures) => failures.map(({ plugin, error }) => ({ plugin, error: wireError(error) }));
	try {
		const report = await runtime.load(host, { kind: request.kind, core, scripts: request.scripts, modules }, traced);
		return { failed: wireFailures(report.failed) };
	} catch (error) {
		// A required root's plugins failed once the host's load settled (runtime.ts's PluginError): the host stays
		// loaded, and the Go side raises the error with these (`required` is never empty then).
		if (!(error instanceof PluginError)) throw error;
		return { failed: wireFailures(error.failed), required: wireFailures(error.required) };
	}
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
	hosted.scripts = request.scripts;
	const report = await hosted.runtime.reload(hosted.host, request.scripts, context);
	return {
		plugins: report.plugins,
		failed: report.failed.map(({ plugin, error }) => ({ plugin, error: wireError(error) })),
		objects: report.objects.map(wireObject),
	};
}

/**
 * As a worker connects to its parent (node/workers/main.ts): the Go side uses the other end of the link, as the server
 * of a worker does, and `peer` is who the Go side is, as this host stamps it. Once; the host's heartbeat starts with the link.
 */
function attach() {
	if (hosted === undefined) throw new Error("attach: no host is loaded");
	if (hosted.attached) return;
	hosted.attached = true;
	hosted.host.connect(link, { principal: hosted.peer, trusted: true }, hosted.base);
}

async function revoke(request, context) {
	if (hosted === undefined) throw new Error("revoke: no host is loaded");
	hosted.host.revoke(request.revocation, context);
	return {};
}

// The reference RPC and contracts as data (bridge.mjs, contracts.mjs): the control requests of `NodeHost.Get` and the Go host's `Bridged` facets.
const { createBridge, GO_PACKAGE } = await import("./bridge.mjs");
const bridge = createBridge({ port, wireError, host: () => hosted?.host });
const { testOperations } = await import("./testing.mjs");
const operations = { load, reload, revoke, ...bridge.operations, ...testOperations({ hosted: () => hosted, chord: chordNext }) };

port.on("message", (message) => {
	if (message instanceof Uint8Array) return;
	if (message.op === "answer") {
		callbacks.answer(message);
		return;
	}
	if (message.op === "abort") {
		running.get(message.target)?.abort(new Error("the request was aborted by the Go side"));
		return;
	}
	if (message.op === "attach") {
		attach();
		return;
	}
	if (message.op === "close") {
		// The host's close ends its links, this process's channel with them; the app's store is closed after the host, as
		// Pi's apps do (a host that failed to close still lets the store close); the process ends once both are closed.
		// `closing` tells the Go side the loop is alive and the close under way: it waits the host's closeTimeout for the exit.
		port.control({ event: "closing" });
		(hosted?.host.close(BACKGROUND_CONTEXT) ?? Promise.resolve())
			.finally(() => hosted?.store?.close(BACKGROUND_CONTEXT))
			.finally(() => process.exit(0));
		return;
	}
	if (bridge.handle(message)) return;
	const operation = operations[message.op];
	const controller = new AbortController();
	running.set(message.id, controller);
	const context = withAbortSignal(controller.signal, hosted?.base ?? BACKGROUND_CONTEXT);
	const answer = operation === undefined ? Promise.reject(new Error(`unknown control request ${message.op}`)) : operation(message, context);
	answer.then(
		(result) => ({ id: message.id, ok: true, result }),
		(error) => ({ id: message.id, ok: false, error: wireError(error) }),
	).then((reply) => {
		running.delete(message.id);
		port.control(reply);
		// The modules the request imported (a load's plugins, a reload's) are compiled: the reply is out before the cache is written.
		if (message.op === "load" || message.op === "reload") flushCompileCache();
	});
});

port.control({ event: "started" });
// After `started` is out: a Go side that waits for it (a spare process is started ahead of a load) is not held for the write.
setImmediate(flushCompileCache);
