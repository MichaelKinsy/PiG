const RETRYABLE_STATUS_CODES = new Set([408, 425, 429, 500, 502, 503, 504]);
/**
 * Fetch a management HTTP resource with a bounded immediate retry.
 *
 * This is intentionally a transport-level helper for idempotent management
 * requests (version checks, catalogs, and downloads). It must not be used for
 * agent/model operations: those can fail after the HTTP request starts and are
 * retried by their semantic caller instead.
 *
 * Caller cancellation and timeoutMs are terminal. attemptTimeoutMs aborts
 * only the current attempt so a hung connection can be retried.
 */
export async function fetchWithRetry(input, init = undefined, options = {}) {
    const maxRetries = options.maxRetries === undefined || !Number.isFinite(options.maxRetries)
        ? 2
        : Math.max(0, Math.floor(options.maxRetries));
    const retryOnStatus = options.retryOnStatus ?? true;
    const parentSignal = init?.signal ?? undefined;
    const timeoutSignal = options.timeoutMs !== undefined && options.timeoutMs > 0 ? AbortSignal.timeout(options.timeoutMs) : undefined;
    const attemptTimeoutMs = options.attemptTimeoutMs !== undefined && options.attemptTimeoutMs > 0 ? options.attemptTimeoutMs : undefined;
    for (let attempt = 0;; attempt++) {
        parentSignal?.throwIfAborted();
        timeoutSignal?.throwIfAborted();
        const attemptTimeoutSignal = attemptTimeoutMs ? AbortSignal.timeout(attemptTimeoutMs) : undefined;
        const signals = [parentSignal, timeoutSignal, attemptTimeoutSignal].filter((signal) => signal !== undefined);
        const signal = signals.length > 1 ? AbortSignal.any(signals) : signals[0];
        try {
            const response = await fetch(input, signal ? { ...init, signal } : init);
            const shouldRetry = retryOnStatus && RETRYABLE_STATUS_CODES.has(response.status) && attempt < maxRetries;
            if (!shouldRetry)
                return response;
            try {
                await response.body?.cancel();
            }
            catch {
                // The response is being discarded before a retry. There is nothing useful to
                // do if cancelling its body also fails.
            }
        }
        catch (error) {
            const attemptTimedOut = attemptTimeoutSignal?.aborted === true && !parentSignal?.aborted && !timeoutSignal?.aborted;
            if (parentSignal?.aborted ||
                timeoutSignal?.aborted ||
                (error instanceof Error &&
                    error.name === "AbortError" &&
                    !attemptTimedOut &&
                    timeoutSignal === undefined) ||
                attempt >= maxRetries) {
                throw error;
            }
        }
    }
}
//# sourceMappingURL=management-http.js.map