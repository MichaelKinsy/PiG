// The control requests that carry contracts as data and references across the boundary (docs/plan/chord-plugins-node-evaluator.md
// 10.1 to 10.4).
//
// Go to Node: `get`, `value`, `watch` and `call` serve `NodeHost.Get` over Pi's own `host.get`: the Go side holds an object of
// a local type that the plugins serve. Each is answered with one reply, as every control request is, and runs with the
// request's context (the Go side's abort aborts it). `unwatch` and `release` are notifications (no reply). Node to Go,
// unrequested and in the order they happen on the one channel: `frame` (one call of a watch's listener), `watchEnd` (the
// watch ended, and why) and `state` (a reference's state changed).
import { BACKGROUND_CONTEXT, withAbortSignal } from "@earendil-works/chord/context";
import { contractOf, operations as contractOperations } from "./contracts.mjs";

/** A reference's state as data: its kind and the fields of that kind (the causes and errors as ChordError data). */
function wireState(state) {
	switch (state.kind) {
		case "failed":
			return {
				kind: "failed",
				cause: { kind: state.cause.kind, error: state.cause.error.toJSON() },
				...(state.retryAt === undefined ? {} : { retryAt: state.retryAt }),
			};
		case "ended":
			return { kind: "ended", error: state.error.toJSON() };
		default:
			return { kind: state.kind };
	}
}

/**
 * The bridge of one driver: `port` is its control channel (`control(message)`), `wireError` turns an error into data and
 * `host()` is the process's host (undefined before `load`).
 */
export function createBridge({ port, wireError, host }) {
	/** The references the Go side holds, by handle: the reference, the members of its contract and its state watch. */
	const handles = new Map();
	/** The watches the Go side holds, by the id the Go side chose: the watch and the controller of its context. */
	const watches = new Map();

	function entry(handle) {
		const found = handles.get(handle);
		if (found === undefined) throw new Error(`no reference ${handle} is held`);
		return found;
	}

	/** The member `name` of the held contract if it is of `kind` (`state`, `keyed` or `args`: a method); else an error. */
	function member(held, name, kind) {
		const spec = Object.hasOwn(held.members, name) ? held.members[name] : undefined;
		if (spec === undefined || !Object.hasOwn(spec, kind)) {
			throw new Error(`${name}: not a ${kind === "args" ? "method" : `${kind} member`} of the reference`);
		}
		return spec;
	}

	function wireCall(call) {
		if (call.kind === "failed") return { kind: "failed", error: wireError(call.error) };
		return { kind: "value", value: call.value, ops: call.ops, retired: call.retired };
	}

	function wireEnd(end) {
		return end.error === undefined ? { kind: end.kind } : { kind: end.kind, error: wireError(end.error) };
	}

	/**
	 * Runs `then` once the reply of the request that is answering was written: the frames of a watch follow its reply on the
	 * channel, as the reply is written in the microtasks after the request's promise settles and a macrotask comes after.
	 */
	function afterReply(then) {
		setImmediate(then);
	}

	async function get(request, context) {
		const target = host();
		if (target === undefined) throw new Error("get: no host is loaded");
		const contract = contractOf(request.contract);
		if (contract.kind !== "local") throw new Error(`${contract.name}: only a local type is held through the control channel`);
		const options = request.readyTimeout === undefined ? undefined : { readyTimeout: request.readyTimeout };
		const reference = await target.get(contract, request.params, context, options);
		// The handle is the Go side's: it registered the reference before it sent this request.
		const { handle } = request;
		const held = { reference, members: request.contract.normal.members, state: undefined };
		handles.set(handle, held);
		// The state's changes follow in order, numbered from 1 (the reply shows change 0): the Go side keeps the latest.
		held.state = await reference.state.watch(BACKGROUND_CONTEXT);
		const state = wireState(held.state.value);
		afterReply(() => {
			let seq = 0;
			held.state.start(async (call) => {
				if (call.kind === "value") port.control({ event: "state", handle, seq: ++seq, state: wireState(call.value) });
			});
		});
		return { state };
	}

	async function value(request, context) {
		const held = entry(request.handle);
		const name = request.member;
		if (request.key !== undefined) {
			member(held, name, "keyed");
			const found = await held.reference[name].get(request.key, context);
			return found === undefined ? { found: false } : { found: true, value: found };
		}
		member(held, name, "state");
		return { value: held.reference[name].value };
	}

	async function watch(request, context) {
		const held = entry(request.handle);
		const name = request.member;
		const controller = new AbortController();
		const onAbort = () => controller.abort(context.abortSignal.reason);
		context.abortSignal.addEventListener("abort", onAbort, { once: true });
		const watchContext = withAbortSignal(controller.signal, BACKGROUND_CONTEXT);
		let acquired;
		try {
			if (request.key !== undefined) {
				member(held, name, "keyed");
				acquired = await held.reference[name].watch(request.key, watchContext);
			} else {
				member(held, name, "state");
				acquired = await held.reference[name].watch(watchContext);
			}
		} catch (error) {
			controller.abort(error);
			throw error;
		} finally {
			context.abortSignal.removeEventListener("abort", onAbort);
		}
		if (acquired === undefined) return { absent: true };
		const id = request.watch;
		watches.set(id, { watch: acquired, controller });
		afterReply(() => {
			acquired.start(async (call) => {
				port.control({ event: "frame", watch: id, call: wireCall(call) });
			});
			void acquired.closed.then((end) => {
				if (watches.get(id)?.watch !== acquired) return;
				watches.delete(id);
				port.control({ event: "watchEnd", watch: id, end: wireEnd(end) });
			});
		});
		return { value: acquired.value };
	}

	/** Notification `unwatch`: the Go side stopped the watch (or released its reference). */
	async function unwatch(message) {
		const found = watches.get(message.watch);
		if (found === undefined) return;
		watches.delete(message.watch);
		await found.watch.stop();
		found.controller.abort(new Error("unwatched"));
	}

	async function call(request, context) {
		const held = entry(request.handle);
		const name = request.method;
		const spec = member(held, name, "args");
		const noArgs = spec.args.type === "null" && Object.keys(spec.args).length === 1;
		const result = await (noArgs ? held.reference[name](context) : held.reference[name](request.args, context));
		return { result: result === undefined ? null : result };
	}

	/** Notification `release`: the Go side released the reference; its watches end `ended` with it. */
	async function release(message) {
		const held = handles.get(message.handle);
		if (held === undefined) return;
		handles.delete(message.handle);
		held.reference.release();
		await held.state.stop();
	}

	const notifications = { unwatch, release };
	return {
		operations: { ...contractOperations, get, value, watch, call },
		/**
		 * Handles a control message that is no request of `operations`: a notification of the Go side (no id, no reply), and
		 * returns whether it was one.
		 */
		handle(message) {
			const notification = notifications[message.op];
			if (notification === undefined) return false;
			notification(message).catch((error) => port.control({ event: "fatal", error: wireError(error) }));
			return true;
		},
	};
}
