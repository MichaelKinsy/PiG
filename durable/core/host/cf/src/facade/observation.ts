// Ports packages/durable/src/session/session.ts guardObserver and observation: the guard of a synchronous Observer. The attached
// state source is the host's own Chord bridge over the core's `published` notices (WatchHub); Pi's watches are Chord's `Watch` and
// `NativeWatch`, so the host builds them directly.
import type { Context, ReplicatedStateSource, ReplicatedStateSourceAttachment, ReplicatedStateSourceFrame } from "@earendil-works/chord";
import type { Op } from "@earendil-works/chord/delta";
import { contained } from "./session/session.ts";
import type { Observation, Observer } from "./watch-types.ts";

export type ObservedDocumentValue = Readonly<{ [key: string]: unknown }> | null;

/** Canonical terminal update for a retired document incarnation. */
export const RETIREMENT_OPERATIONS: readonly Op[] = [["r", null]];

/**
 * Guard `observer` as session.ts guardObserver does: a throw from `advance` is reported, detaches it and ends it with the
 * error; `end` and `detach()` release it from its source once, and nothing reaches it afterwards. `detach` is the source's;
 * the returned `detach()` is the observation's.
 */
export function guardObserver<T>(
	observer: Observer<T>,
	detach: () => void,
	report: (error: unknown) => void,
): Observer<T> & { detach(): void } {
	type Attached = {
		readonly observer: Observer<T>;
		readonly detach: () => void;
		readonly report: (error: unknown) => void;
	};
	let attached: Attached | undefined = { observer, detach, report };
	const release = (): Attached | undefined => {
		const released = attached;
		attached = undefined;
		released?.detach();
		return released;
	};
	return {
		advance: (change, context) => {
			const current = attached;
			if (current === undefined) return;
			try {
				current.observer.advance(change, context);
			} catch (error) {
				current.report(error);
				if (release() !== undefined) contained(() => current.observer.end({ error }), current.report);
			}
		},
		end: (failure) => {
			const ending = release();
			if (ending !== undefined) contained(() => ending.observer.end(failure), ending.report);
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

/**
 * Session-to-Chord bridge owned one-to-one by one attached state: a document, or a conversation view. A `null` value
 * retires it. @internal
 */
export class CommittedStateSource<T = ObservedDocumentValue> implements ReplicatedStateSource<T> {
	readonly #attachments = new Set<SessionSourceAttachment<T>>();
	#release: (() => void) | undefined;
	/** Released on disposal. */
	#value: T | undefined;
	#cursor = 0;
	#retired = false;
	#closed = false;

	constructor(value: T, release: () => void) {
		this.#value = value;
		this.#release = release;
	}

	attach(): ReplicatedStateSourceAttachment<T> {
		if (this.#closed) throw new Error("State source is closed");
		const attachment = new SessionSourceAttachment({ value: this.#value as T, cursor: this.#cursor }, () => {
			this.#attachments.delete(attachment);
			if (this.#attachments.size === 0) this.#finishDisposal();
		});
		this.#attachments.add(attachment);
		return attachment;
	}

	advance(value: T, ops: readonly Op[], context: Context): void {
		if (this.#closed || this.#retired) return;
		this.#value = value;
		this.#cursor += 1;
		if (value === null) this.#retired = true;
		const frame: ReplicatedStateSourceFrame<T> = {
			cursor: this.#cursor,
			value,
			ops,
			context,
		};
		for (const attachment of [...this.#attachments]) attachment.publish(frame);
	}

	closeSession(): void {
		if (this.#closed) return;
		for (const attachment of [...this.#attachments]) attachment.dispose();
		this.#finishDisposal();
	}

	#finishDisposal(): void {
		if (this.#closed) return;
		this.#closed = true;
		this.#value = undefined;
		const release = this.#release;
		this.#release = undefined;
		release?.();
	}
}

class SessionSourceAttachment<T> implements ReplicatedStateSourceAttachment<T> {
	readonly snapshot: { readonly value: T; readonly cursor: number };
	#release: (() => void) | undefined;
	readonly #frames: ReplicatedStateSourceFrame<T>[] = [];
	#listener: ((frame: ReplicatedStateSourceFrame<T>) => void) | undefined;
	#activated = false;
	#disposed = false;
	#scheduled = false;
	#delivering = false;

	constructor(snapshot: { readonly value: T; readonly cursor: number }, release: () => void) {
		this.snapshot = snapshot;
		this.#release = release;
	}

	activate(listener: (frame: ReplicatedStateSourceFrame<T>) => void): void {
		if (this.#activated) throw new Error("State attachment is already active");
		if (this.#disposed) throw new Error("State attachment is disposed");
		this.#activated = true;
		this.#listener = listener;
		this.#drain();
	}

	publish(frame: ReplicatedStateSourceFrame<T>): void {
		if (this.#disposed) return;
		this.#frames.push(frame);
		if (!this.#activated || this.#delivering || this.#scheduled) return;
		this.#scheduled = true;
		queueMicrotask(() => {
			this.#scheduled = false;
			if (this.#disposed) return;
			try {
				this.#drain();
			} catch {
				// Chord's frame listener contains source-contract failures. Isolate an
				// unexpected direct listener failure to this attachment as well.
				this.dispose();
			}
		});
	}

	dispose(): void {
		if (this.#disposed) return;
		this.#disposed = true;
		this.#frames.length = 0;
		this.#listener = undefined;
		const release = this.#release;
		this.#release = undefined;
		release?.();
	}

	#drain(): void {
		const listener = this.#listener;
		if (listener === undefined || this.#delivering || this.#disposed) return;
		this.#delivering = true;
		try {
			while (!this.#disposed) {
				const frame = this.#frames.shift();
				if (frame === undefined) break;
				listener(frame);
			}
		} finally {
			this.#delivering = false;
		}
	}
}
