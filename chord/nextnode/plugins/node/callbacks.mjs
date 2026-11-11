// Calls from this process to the Go side (docs/plan/chord-plugins-node-evaluator.md, 10.1), over the control channel of
// host.mjs. There are two kinds.
//
// `call(name, args, signal)` is asynchronous: `{ event: "call", call, name, args }` is answered by `{ op: "answer", id: call, ok,
// result | error }` in any order relative to other calls, and settles the returned promise. An abort of `signal` rejects it
// with the signal's reason at once and sends `{ event: "cancel", call }`, which cancels the Go function's context; the answer
// that follows is dropped.
//
// `callSync(name, args)` is synchronous: it returns the Go function's result, or throws its error, before it returns. It
// needs a second connection to the socket (the channel's own is the host thread's, which is blocked): `startSync` starts a
// worker thread that owns the second connection, and `callSync` hands it the request and blocks the host thread with
// `Atomics.wait` until the worker stored the answer. The Go side serves it on its own connection, so the call never passes
// through this thread's event loop.
import { MessageChannel, receiveMessageOnPort, Worker } from "node:worker_threads";

const { ChordError } = await import(new URL("./pi/chord/src/next/index.ts", import.meta.url).href);

/** The control channel `attach` was given (host.mjs's framed port). */
let channel;
let nextCall = 0;
/** The asynchronous calls not answered yet, by id. */
const pending = new Map();

/** Gives this module the control channel that carries `call`s, and the `answer`s to them. */
export function attach(port) {
	channel = port;
}

/** The error a Go function's failure is: a ChordError when the Go error was one, else an Error with its message. */
function failure(error) {
	if (error.chord !== undefined) return new ChordError(error.chord);
	const made = new Error(error.message);
	if (error.name !== undefined) made.name = error.name;
	return made;
}

function aborted(signal) {
	const reason = signal.reason;
	return reason instanceof Error ? reason : new DOMException("The operation was aborted", "AbortError");
}

/** The abort signal a call's context argument names: an AbortSignal, or a chord Context (its `abortSignal`). */
export function signalOf(context) {
	if (context instanceof AbortSignal) return context;
	return context?.abortSignal;
}

/**
 * Calls the Go function `name` with the JSON `args` and resolves with its JSON result. `signal` (optional) is the abort of
 * the request the call belongs to: it rejects the call at once with its reason and cancels the Go function's context.
 */
export function call(name, args, signal) {
	return new Promise((resolve, reject) => {
		if (signal?.aborted) {
			reject(aborted(signal));
			return;
		}
		const id = ++nextCall;
		const onAbort = () => {
			pending.delete(id);
			channel.control({ event: "cancel", call: id });
			reject(aborted(signal));
		};
		signal?.addEventListener("abort", onAbort, { once: true });
		pending.set(id, {
			resolve: (result) => {
				signal?.removeEventListener("abort", onAbort);
				resolve(result);
			},
			reject: (error) => {
				signal?.removeEventListener("abort", onAbort);
				reject(error);
			},
		});
		channel.control({ event: "call", call: id, name, args: args ?? null });
	});
}

/** Settles the call an `answer` message names; an answer to a call that was aborted is dropped. */
export function answer(message) {
	const waiting = pending.get(message.id);
	if (waiting === undefined) return;
	pending.delete(message.id);
	if (message.ok) waiting.resolve(message.result);
	else waiting.reject(failure(message.error));
}

/** The synchronous side: set by `startSync`. */
let sync;

/**
 * Starts the worker thread that owns the second connection (`address` is the socket the channel is connected to, `token`
 * what the Go side checks the connection by) and resolves once that connection is made.
 */
export async function startSync(address, token) {
	if (sync !== undefined) return;
	const state = new Int32Array(new SharedArrayBuffer(4));
	const { port1, port2 } = new MessageChannel();
	const worker = new Worker(new URL("./syncworker.mjs", import.meta.url), {
		workerData: { address, token, state, results: port2 },
		transferList: [port2],
	});
	// The worker must not keep the process alive: the channel's end ends the process.
	worker.unref();
	await new Promise((resolve, reject) => {
		worker.once("message", resolve);
		worker.once("error", reject);
	});
	sync = { worker, state, results: port1, next: 0 };
}

/**
 * Calls the Go function `name` with the JSON `args` and returns its JSON result, or throws its error; the host thread is
 * blocked until the Go side answered. The function must not call back into this process (the Go side refuses such calls).
 */
export function callSync(name, args) {
	if (sync === undefined) throw new Error("callSync: no synchronous connection was started");
	const { worker, state, results } = sync;
	Atomics.store(state, 0, 0);
	worker.postMessage({ id: ++sync.next, name, args: args ?? null });
	Atomics.wait(state, 0, 0);
	const { message } = receiveMessageOnPort(results);
	if (!message.ok) throw failure(message.error);
	return message.result;
}

/**
 * `fn` memoized by the canonical JSON of its principal argument, in a map bounded to `limit` entries (the oldest dropped
 * first): a synchronous call costs tens of microseconds, and the host asks for the key of every principal object it decodes,
 * which for one principal is one object per request. `fn` is a pure function of its principal, as pi-chord's per-object cache
 * of the key assumes. `canonical` is the canonical JSON of a principal.
 */
export function memoizedByPrincipal(canonical, fn, limit = 4096) {
	const memo = new Map();
	return (principal) => {
		const key = canonical(principal);
		if (memo.has(key)) return memo.get(key);
		const value = fn(principal);
		if (memo.size >= limit) memo.delete(memo.keys().next().value);
		memo.set(key, value);
		return value;
	};
}
