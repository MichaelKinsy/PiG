const ABORT_SIGNAL_CONTEXT_KEY = Object.freeze({
    token: Symbol("chord.abortSignal"),
});
class BaseContext {
    get abortSignal() {
        return this.value(ABORT_SIGNAL_CONTEXT_KEY);
    }
}
class EmptyContext extends BaseContext {
    #name;
    constructor(name) {
        super();
        this.#name = name;
    }
    value(_key) {
        return undefined;
    }
    toString() {
        return this.#name;
    }
}
class ContextValue extends BaseContext {
    #parent;
    #key;
    #value;
    constructor(parent, key, value) {
        super();
        this.#parent = parent;
        this.#key = key;
        this.#value = value;
    }
    value(key) {
        if (key.token === this.#key.token)
            return this.#value;
        return this.#parent.value(key);
    }
    toString() {
        return `${this.#parent}.WithValue(${this.#key.token.description ?? "anonymous"})`;
    }
}
export const BACKGROUND_CONTEXT = new EmptyContext("[Context BACKGROUND_CONTEXT]");
export const TODO_CONTEXT = new EmptyContext("[Context TODO_CONTEXT]");
export function createContextKey(description) {
    return Object.freeze({ token: Symbol(description) });
}
/** Derive a context containing one additional or replaced value. */
export function withContextValue(key, value, parent) {
    return new ContextValue(parent, key, value);
}
/**
 * Derive a context cancelled by either the parent signal or the supplied signal.
 * The parent context remains unchanged.
 */
export function withAbortSignal(signal, context) {
    const parentSignal = context.abortSignal;
    const combined = parentSignal === undefined ? signal : AbortSignal.any([parentSignal, signal]);
    return withContextValue(ABORT_SIGNAL_CONTEXT_KEY, combined, context);
}
/** Derive a context retaining all values except caller cancellation. Intended for mandatory cleanup only. */
export function withoutAbortSignal(context) {
    return withContextValue(ABORT_SIGNAL_CONTEXT_KEY, undefined, context);
}
/** Derive an independently cancellable child context. */
export function withCancel(context) {
    const controller = new AbortController();
    return {
        context: withAbortSignal(controller.signal, context),
        cancel: (reason) => controller.abort(reason),
    };
}
/**
 * Observe a promise until it settles or the invocation is cancelled.
 * Cancellation rejects only this waiter; it does not cancel the underlying promise.
 */
export function awaitWithContext(promise, context) {
    const signal = context.abortSignal;
    if (signal === undefined)
        return promise;
    if (signal.aborted)
        return Promise.reject(abortError(signal));
    return new Promise((resolve, reject) => {
        const onAbort = () => reject(abortError(signal));
        signal.addEventListener("abort", onAbort, { once: true });
        void promise.then((value) => {
            signal.removeEventListener("abort", onAbort);
            resolve(value);
        }, (error) => {
            signal.removeEventListener("abort", onAbort);
            reject(error);
        });
    });
}
function abortError(signal) {
    const reason = signal.reason;
    return reason instanceof Error ? reason : new DOMException("The operation was aborted", "AbortError");
}
//# sourceMappingURL=index.js.map