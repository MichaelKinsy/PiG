class FifoQueue {
    incoming = [];
    outgoing = [];
    get length() {
        return this.incoming.length + this.outgoing.length;
    }
    enqueue(value) {
        this.incoming.push(value);
    }
    dequeue() {
        if (this.outgoing.length === 0) {
            while (this.incoming.length > 0) {
                this.outgoing.push(this.incoming.pop());
            }
        }
        return this.outgoing.pop();
    }
}
// Generic event stream class for async iteration
export class EventStream {
    queue = new FifoQueue();
    waiting = new FifoQueue();
    done = false;
    finalResultPromise;
    resolveFinalResult;
    isComplete;
    extractResult;
    constructor(isComplete, extractResult) {
        this.isComplete = isComplete;
        this.extractResult = extractResult;
        this.finalResultPromise = new Promise((resolve) => {
            this.resolveFinalResult = resolve;
        });
    }
    push(event) {
        if (this.done)
            return;
        if (this.isComplete(event)) {
            this.done = true;
            this.resolveFinalResult(this.extractResult(event));
        }
        // Deliver to waiting consumer or queue it
        const waiter = this.waiting.dequeue();
        if (waiter) {
            waiter({ value: event, done: false });
        }
        else {
            this.queue.enqueue(event);
        }
    }
    end(result) {
        this.done = true;
        if (result !== undefined) {
            this.resolveFinalResult(result);
        }
        // Notify all waiting consumers that we're done
        while (this.waiting.length > 0) {
            const waiter = this.waiting.dequeue();
            waiter({ value: undefined, done: true });
        }
    }
    async *[Symbol.asyncIterator]() {
        while (true) {
            if (this.queue.length > 0) {
                yield this.queue.dequeue();
            }
            else if (this.done) {
                return;
            }
            else {
                const result = await new Promise((resolve) => this.waiting.enqueue(resolve));
                if (result.done)
                    return;
                yield result.value;
            }
        }
    }
    result() {
        return this.finalResultPromise;
    }
}
export class AssistantMessageEventStream extends EventStream {
    constructor() {
        super((event) => event.type === "done" || event.type === "error", (event) => {
            if (event.type === "done") {
                return event.message;
            }
            else if (event.type === "error") {
                return event.error;
            }
            throw new Error("Unexpected event type for final result");
        });
    }
}
/** Factory function for AssistantMessageEventStream (for use in extensions) */
export function createAssistantMessageEventStream() {
    return new AssistantMessageEventStream();
}
//# sourceMappingURL=event-stream.js.map