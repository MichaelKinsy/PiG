// Calls from this process to the Go side (docs/plan/chord-plugins-node-evaluator.md, 10.1), over the control channel of
// host.mjs.
//
// `call(name, args, signal)` is asynchronous: `{ event: "call", call, name, args }` is answered by `{ op: "answer", id: call, ok,
// result | error }` in any order relative to other calls, and settles the returned promise. An abort of `signal` rejects it
// with the signal's reason at once and sends `{ event: "cancel", call }`, which cancels the Go function's context; the answer
// that follows is dropped.

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
