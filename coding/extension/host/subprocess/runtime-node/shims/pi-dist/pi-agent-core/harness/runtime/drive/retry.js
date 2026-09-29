import { retryDelayMs } from "../../../../pi-ai/sdk-bundle/index.js";
export function retryNotBefore(policy, attempt, now = Date.now()) {
    const sum = now + retryDelayMs(policy, attempt);
    return Number.isSafeInteger(sum) ? sum : Number.MAX_SAFE_INTEGER;
}
export function waitUntil(notBefore, signal) {
    return new Promise((resolve, reject) => {
        let timer;
        const cleanup = () => {
            if (timer !== undefined)
                clearTimeout(timer);
            signal.removeEventListener("abort", onAbort);
        };
        const onAbort = () => {
            cleanup();
            reject(signal.reason);
        };
        const check = () => {
            const remaining = notBefore - Date.now();
            if (remaining <= 0) {
                cleanup();
                resolve();
                return;
            }
            timer = setTimeout(check, Math.min(remaining, 2_147_483_647));
        };
        signal.addEventListener("abort", onAbort, { once: true });
        if (signal.aborted)
            onAbort();
        else
            check();
    });
}
//# sourceMappingURL=retry.js.map