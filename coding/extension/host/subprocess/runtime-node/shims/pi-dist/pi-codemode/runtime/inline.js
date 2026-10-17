import { CodemodeSandboxBase, errorMessage } from "./execution.js";
import { startVm } from "./vm.js";
export const DEFAULT_INTERRUPT_BUDGET = 100_000;
/** Allows recursion about 1,000 calls deep and trips well before a 1 MB host stack overflows. */
export const INLINE_MAX_STACK_SIZE = 256 * 1024;
export function inlineLauncher(wasm, interruptBudget = DEFAULT_INTERRUPT_BUDGET) {
    return async (data, events) => {
        let module;
        try {
            module = await wasm;
        }
        catch (error) {
            throw new Error(`Failed to load QuickJS: ${errorMessage(error)}`);
        }
        let stopped = false;
        let settled = false;
        let polls = 0;
        let exhausted = false;
        const session = await startVm({
            data,
            wasm: module,
            maxStackSize: INLINE_MAX_STACK_SIZE,
            interruptBudget,
            shouldInterrupt: () => {
                if (stopped || settled || exhausted)
                    return true;
                if (++polls <= interruptBudget)
                    return false;
                exhausted = true;
                return true;
            },
            post: (message) => {
                const terminal = message.type === "done" || message.type === "crash";
                // Only the first terminal message counts: a crash caused by interrupting a settled VM is noise.
                if (terminal && settled)
                    return;
                if (terminal)
                    settled = true;
                queueMicrotask(() => {
                    if (stopped)
                        return;
                    // The interrupt surfaces as a crash or a failed done; report what caused it instead.
                    if (exhausted && (message.type === "done" || message.type === "crash")) {
                        events.failure({
                            kind: "timeout",
                            message: `Execution exceeded its interrupt budget of ${interruptBudget}`,
                        });
                        return;
                    }
                    events.message(message);
                });
            },
        });
        return {
            post: (message) => {
                if (!stopped)
                    session.deliver(message);
            },
            stop: async () => {
                if (stopped)
                    return;
                stopped = true;
                session.dispose();
            },
        };
    };
}
/**
 * Runs each execution's QuickJS VM on the calling thread. Works in any runtime with WebAssembly
 * and microtasks; needs no worker threads, files, or Node built-ins. A script that computes
 * blocks the caller's thread until it awaits a tool or uses up its interrupt budget, so prefer
 * `CodemodeSandbox` where worker threads exist, and `RemoteCodemodeSandbox` to keep scripts off a
 * thread that must stay responsive (such as a Durable Object that serves a session).
 */
export class InlineCodemodeSandbox extends CodemodeSandboxBase {
    constructor(options) {
        super(options, inlineLauncher(options.wasm, options.interruptBudget));
    }
}
//# sourceMappingURL=inline.js.map