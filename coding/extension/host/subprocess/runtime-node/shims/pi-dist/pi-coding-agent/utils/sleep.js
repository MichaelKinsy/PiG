/**
 * Sleep helper that respects abort signal.
 */
export function sleep(ms, signal) {
    return new Promise((resolve, reject) => {
        if (signal?.aborted) {
            reject(new Error("Aborted"));
            return;
        }
        const timeout = setTimeout(resolve, ms);
        signal?.addEventListener("abort", () => {
            clearTimeout(timeout);
            reject(new Error("Aborted"));
        });
    });
}
/**
 * Wait until stdout has written everything queued before this call, for at most one second (Pi 7c05885bc, utils/flush-stdout.ts).
 */
export function flushStdout() {
    return new Promise((resolve) => {
        const finish = (error) => {
            clearTimeout(timer);
            if (!error)
                process.stdout.off("error", finish);
            resolve();
        };
        const timer = setTimeout(finish, 1000);
        process.stdout.on("error", finish);
        process.stdout.write("", finish);
    });
}
//# sourceMappingURL=sleep.js.map