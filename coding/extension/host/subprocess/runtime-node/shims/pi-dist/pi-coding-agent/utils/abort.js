function abortReason(signal) {
    if (signal.reason !== undefined)
        return signal.reason;
    const error = new Error("The operation was aborted");
    error.name = "AbortError";
    return error;
}
/** Normalize an optional public signal without imposing a deadline. */
export function operationSignal(signal) {
    return signal ?? new AbortController().signal;
}
/** Stop waiting on abort while observing the abandoned operation through settlement. */
export function raceWithAbortSignal(operation, signal) {
    if (!signal)
        return operation;
    if (signal.aborted) {
        void operation.catch(() => { });
        return Promise.reject(abortReason(signal));
    }
    return new Promise((resolve, reject) => {
        let settled = false;
        const cleanup = () => signal.removeEventListener("abort", onAbort);
        const onAbort = () => {
            if (settled)
                return;
            settled = true;
            cleanup();
            reject(abortReason(signal));
        };
        signal.addEventListener("abort", onAbort, { once: true });
        void operation.then((value) => {
            if (settled)
                return;
            settled = true;
            cleanup();
            resolve(value);
        }, (error) => {
            if (settled)
                return;
            settled = true;
            cleanup();
            reject(error);
        });
        if (signal.aborted)
            onAbort();
    });
}
//# sourceMappingURL=abort.js.map