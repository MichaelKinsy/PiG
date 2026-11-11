// Ports packages/durable/src/session/session.ts guardObserver and observation: the guard of a synchronous Observer. Pi's watches are
// Chord's `Watch` and `NativeWatch`, so the host builds them directly.
import type { Context } from "@earendil-works/chord/context";
import type { WatchEnd } from "@earendil-works/chord/watch";
import { contained } from "./session/session.ts";
import type { Observation } from "./watch-types.ts";

/** A listener's throw as the `Error` a `listener_error` end carries, as Chord's watch makes it. */
function toError(error: unknown): Error {
	if (error instanceof Error) return error;
	try {
		return new Error(String(error));
	} catch {
		return new Error(Object.prototype.toString.call(error));
	}
}

/** The end of an observer whose source closed. */
export const ENDED: WatchEnd = { kind: "ended" };

/** A synchronous observer as the host attaches it: `Observer` (`advance` gets a `WatchCall`) or `CommitObserver`. */
type SyncObserver<A> = { advance(call: A, context: Context): void; end(end: WatchEnd): void };

/** A guarded observer (`guardObserver()`) with its observation's detach. */
export type GuardedObserver<A> = SyncObserver<A> & { detach(): void };

/**
 * Guard `observer` as session.ts guardObserver does: one that throws, or whose `advance` returns a promise that rejects, is
 * reported through `report`, detached, and ended `listener_error`; `end` and `detach()` release it from its source once, and
 * nothing reaches it afterwards. `detach` is the source's; the returned `detach()` is the observation's.
 */
export function guardObserver<A>(
	observer: SyncObserver<A>,
	detach: () => void,
	report: (error: unknown, context?: Context) => void,
): GuardedObserver<A> {
	type Attached = {
		readonly observer: SyncObserver<A>;
		readonly detach: () => void;
		readonly report: (error: unknown, context?: Context) => void;
	};
	let attached: Attached | undefined = { observer, detach, report };
	/** Detach from the source once, and hand back what it was attached with. */
	const release = (): Attached | undefined => {
		const released = attached;
		attached = undefined;
		released?.detach();
		return released;
	};
	/** Report a throw of `current`'s `advance`, then detach and end it, unless that happened meanwhile. */
	const fault = (current: Attached, error: unknown, context: Context): void => {
		current.report(error, context);
		if (release() !== undefined) {
			const end: WatchEnd = { kind: "listener_error", error: toError(error) };
			contained(() => current.observer.end(end), current.report);
		}
	};
	return {
		advance: (call, context) => {
			const current = attached;
			if (current === undefined) return;
			try {
				const result: unknown = current.observer.advance(call, context);
				// Any thenable, as `contained()` takes it: a rejection is the observer's throw, after the fact.
				if (typeof (result as { then?: unknown } | null)?.then === "function") {
					Promise.resolve(result).catch((error: unknown) => fault(current, error, context));
				}
			} catch (error) {
				fault(current, error, context);
			}
		},
		end: (end) => {
			const ending = release();
			if (ending !== undefined) contained(() => ending.observer.end(end), ending.report);
		},
		detach: () => {
			release();
		},
	};
}

/** The `Observation` of a guarded observer attached at `value`. */
export function observation<T>(value: T, guarded: { detach(): void }): Observation<T> {
	return { value, detach: () => guarded.detach() };
}
