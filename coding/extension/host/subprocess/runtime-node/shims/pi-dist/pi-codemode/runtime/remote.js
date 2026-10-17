import { CodemodeSandboxBase, errorMessage } from "./execution.js";
import { DEFAULT_INTERRUPT_BUDGET, inlineLauncher } from "./inline.js";
import { isWorkerToHostMessage, } from "./protocol.js";
/** Output buffered on the remote before it is flushed without waiting for a call. */
const FLUSH_ITEMS = 1_000;
const FLUSH_CHARS = 1 << 20;
/**
 * Remote side of {@link RemoteCodemodeSandbox}: runs one execution's VM on this thread and
 * forwards everything it reports through `exchange`. Resolves once the VM has stopped and the
 * terminal message has been delivered, or the client asked to stop. Never rejects.
 */
export function serveCodemodeRemote(request, exchange, options) {
    const maxBudget = options.maxInterruptBudget ?? DEFAULT_INTERRUPT_BUDGET;
    const budget = Math.min(request.interruptBudget ?? maxBudget, maxBudget);
    const requestedMemory = request.data.memoryLimitBytes;
    const maxMemory = options.maxMemoryLimitBytes;
    const memoryLimitBytes = requestedMemory === undefined
        ? maxMemory
        : maxMemory === undefined
            ? requestedMemory
            : Math.min(requestedMemory, maxMemory);
    const data = { ...request.data, memoryLimitBytes };
    return new Promise((resolve) => {
        let channel;
        let ended = false;
        let buffered = [];
        let bufferedChars = 0;
        /** Replies that arrived before the launcher returned the channel. */
        const unsent = [];
        const takeBuffer = () => {
            const batch = buffered;
            buffered = [];
            bufferedChars = 0;
            return batch;
        };
        const end = (terminal) => {
            if (ended)
                return;
            ended = true;
            const batch = takeBuffer();
            const delivered = terminal ? exchange([...batch, terminal]).catch(() => null) : Promise.resolve();
            void delivered
                .then(() => channel?.stop())
                .catch(() => undefined)
                .then(() => resolve());
        };
        const reply = (message) => {
            if (ended || message === null)
                return;
            if (message.type === "stop") {
                end(undefined);
                return;
            }
            if (channel)
                channel.post(message);
            else
                unsent.push(message);
        };
        const onMessage = (message) => {
            if (ended)
                return;
            const typed = message;
            switch (typed.type) {
                case "output": {
                    buffered.push(typed);
                    bufferedChars += typed.item.type === "text" ? typed.item.text.length : typed.item.data.length;
                    if (buffered.length >= FLUSH_ITEMS || bufferedChars >= FLUSH_CHARS) {
                        exchange(takeBuffer()).then(reply, () => end(undefined));
                    }
                    break;
                }
                case "started":
                    buffered.push(typed);
                    break;
                // Sent at once: a host replaying a script waits for it to see that the script issued no further call.
                case "call":
                case "idle":
                    exchange([...takeBuffer(), typed]).then(reply, () => end(undefined));
                    break;
                default:
                    end(typed);
            }
        };
        const launch = inlineLauncher(options.wasm, budget);
        Promise.resolve(launch(data, {
            message: onMessage,
            failure: (error) => end({ type: "failure", error }),
        })).then((started) => {
            channel = started;
            if (ended) {
                void started.stop().catch(() => undefined);
                return;
            }
            for (const message of unsent.splice(0))
                started.post(message);
        }, (error) => end({ type: "failure", error: { kind: "sandbox", message: errorMessage(error) } }));
    });
}
function remoteLauncher(remote, interruptBudget) {
    return (data, events) => {
        let stopped = false;
        let settled = false;
        const waiting = new Map();
        const exchange = (messages) => new Promise((resolve) => {
            if (stopped || !Array.isArray(messages)) {
                resolve({ type: "stop" });
                return;
            }
            // Register the waiter first: the host may answer a call synchronously, for example when the
            // tool throws before returning a promise.
            const call = messages.find((message) => isWorkerToHostMessage(message) && message.type === "call");
            if (call?.type === "call")
                waiting.set(call.id, resolve);
            else
                resolve(null);
            for (const message of messages) {
                if (typeof message === "object" && message !== null && message.type === "failure") {
                    settled = true;
                    events.failure(message.error);
                    continue;
                }
                // Malformed messages still go to the host, which fails the execution as a broken bridge.
                events.message(message);
                if (isWorkerToHostMessage(message) && (message.type === "done" || message.type === "crash"))
                    settled = true;
            }
        });
        let target;
        try {
            target = remote();
        }
        catch (error) {
            throw new Error(`Failed to reach the remote sandbox: ${errorMessage(error)}`);
        }
        const request = interruptBudget === undefined ? { data } : { data, interruptBudget };
        Promise.resolve()
            .then(() => target.execute(request, exchange))
            .then(() => {
            if (!settled && !stopped) {
                events.failure({ kind: "sandbox", message: "Remote sandbox returned before the script settled" });
            }
        }, (error) => {
            if (!stopped)
                events.failure({ kind: "sandbox", message: `Remote sandbox failed: ${errorMessage(error)}` });
        });
        return {
            post: (message) => {
                const resolve = waiting.get(message.id);
                if (!resolve)
                    return;
                waiting.delete(message.id);
                resolve(message);
            },
            stop: async () => {
                stopped = true;
                for (const resolve of waiting.values())
                    resolve({ type: "stop" });
                waiting.clear();
            },
        };
    };
}
/**
 * Runs each execution's VM on a remote, usually a Cloudflare Durable Object deployed with
 * `CodemodeSandboxDurableObject`, while tools run here. Same API and results as
 * `CodemodeSandbox`, so it can replace it. Differences: a script that computes without calling a
 * tool is limited by the remote's interrupt budget rather than `timeoutMs`, and after a deadline or
 * abort the remote VM keeps computing until it next calls a tool or uses up that budget.
 */
export class RemoteCodemodeSandbox extends CodemodeSandboxBase {
    constructor(options) {
        super(options, remoteLauncher(options.remote, options.interruptBudget));
    }
}
//# sourceMappingURL=remote.js.map