/** Passive harness event bus with isolated handler failures. */
export class HarnessEventBus {
    listeners = new Map();
    watchListeners = new Set();
    deliveryTail = Promise.resolve();
    closedError;
    on(type, listener) {
        if (this.closedError !== undefined)
            throw this.closedError;
        const wrapped = (event, context) => listener(event, context);
        let listeners = this.listeners.get(type);
        if (listeners === undefined) {
            listeners = new Set();
            this.listeners.set(type, listeners);
        }
        listeners.add(wrapped);
        return () => listeners?.delete(wrapped);
    }
    emit(event, context) {
        return this.emitBatch([event], context);
    }
    /** Bind current recipients and append one contiguous batch to the global delivery tail. */
    emitBatch(events, context) {
        if (this.closedError !== undefined || events.length === 0)
            return Promise.resolve();
        const bound = events.map((event) => {
            const payload = structuredClone(event);
            return { payload, recipients: this.snapshotRecipients(payload) };
        });
        const delivery = this.deliveryTail.then(async () => {
            for (const { payload, recipients } of bound)
                await this.deliver(payload, recipients, true, context);
        });
        this.deliveryTail = delivery.catch(() => { });
        return delivery;
    }
    watch(snapshot, filter, _context, resnapshot) {
        if (this.closedError !== undefined)
            throw this.closedError;
        return this.installWatcher(snapshot, filter, resnapshot);
    }
    async watchFromSnapshot(capture, filter, context) {
        if (this.closedError !== undefined)
            throw this.closedError;
        const watcher = this.installWatcher(undefined, filter, async (captureContext, markBoundary) => {
            const snapshot = await capture(captureContext);
            markBoundary();
            return snapshot;
        });
        try {
            watcher.setSnapshot(await capture(context));
            return watcher;
        }
        catch (error) {
            watcher.unsubscribe();
            throw error;
        }
    }
    close(error) {
        this.closedError ??= error;
        void this.deliveryTail.finally(() => {
            this.listeners.clear();
            this.watchListeners.clear();
        });
    }
    installWatcher(snapshot, filter, resnapshot) {
        let watcher;
        const capture = resnapshot === undefined
            ? undefined
            : async (context) => {
                let marked = false;
                const next = await resnapshot(context, () => {
                    if (marked)
                        throw new Error("Resnapshot boundary was already marked");
                    marked = true;
                    this.enqueueBarrier(() => watcher.markResnapshotBoundary());
                });
                if (!marked)
                    throw new Error("Resnapshot capture did not mark its boundary");
                return next;
            };
        watcher = new BufferedEventWatcher(snapshot, capture, async (error, event, context) => {
            if (event.type === "handler_error")
                return;
            const normalized = error instanceof Error ? error : new Error(String(error));
            const lane = "lane" in event && typeof event.lane === "string" ? event.lane : undefined;
            await this.emit({
                type: "handler_error",
                kind: "event",
                event: event.type,
                error: normalized.message,
                ...(normalized.stack === undefined ? {} : { stack: normalized.stack }),
                ...(lane === undefined ? {} : { lane }),
            }, context);
        });
        const watchListener = (event, context) => {
            if (filter(event))
                watcher.push(event, context);
        };
        this.watchListeners.add(watchListener);
        watcher.setUnsubscribe(() => this.watchListeners.delete(watchListener));
        return watcher;
    }
    enqueueBarrier(barrier) {
        const delivery = this.deliveryTail.then(barrier);
        this.deliveryTail = delivery.catch(() => { });
    }
    snapshotRecipients(event) {
        return [...(this.listeners.get(event.type) ?? []), ...this.watchListeners];
    }
    async deliver(event, recipients, reportErrors, context) {
        for (const listener of recipients) {
            try {
                await listener(structuredClone(event), context);
            }
            catch (error) {
                if (!reportErrors || event.type === "handler_error")
                    continue;
                const normalized = error instanceof Error ? error : new Error(String(error));
                const lane = "lane" in event && typeof event.lane === "string" ? event.lane : undefined;
                const handlerError = {
                    type: "handler_error",
                    kind: "event",
                    event: event.type,
                    error: normalized.message,
                    ...(normalized.stack === undefined ? {} : { stack: normalized.stack }),
                    ...(lane === undefined ? {} : { lane }),
                };
                await this.deliver(handlerError, this.snapshotRecipients(handlerError), false, context);
            }
        }
    }
}
class BufferedEventWatcher {
    snapshot;
    resnapshotCallback;
    onError;
    buffer = [];
    listener;
    unsubscribeCallback;
    deliveryTail = Promise.resolve();
    epoch = 0;
    resnapshotState;
    state = "buffering";
    constructor(snapshot, resnapshot, onError) {
        this.snapshot = snapshot;
        this.resnapshotCallback = resnapshot;
        this.onError = onError;
    }
    setSnapshot(snapshot) {
        this.snapshot = snapshot;
    }
    start(listener) {
        if (this.state !== "buffering")
            throw new Error("WatchHandle.start() may be called only once");
        this.state = "started";
        this.listener = listener;
        const buffered = this.buffer;
        this.buffer = [];
        for (const bufferedEvent of buffered) {
            this.enqueue(bufferedEvent.event, bufferedEvent.context, bufferedEvent.epoch);
        }
    }
    async resnapshot(context) {
        if (this.state === "unsubscribed")
            throw new Error("WatchHandle is unsubscribed");
        if (this.resnapshotCallback === undefined)
            throw new Error("WatchHandle does not support resnapshot");
        if (this.resnapshotState !== undefined)
            throw new Error("WatchHandle resnapshot is already in progress");
        let resolveReached;
        const reached = new Promise((resolve) => {
            resolveReached = resolve;
        });
        const resnapshotState = {
            phase: "dropping",
            held: [],
            reached,
            resolveReached,
        };
        this.epoch++;
        this.resnapshotState = resnapshotState;
        try {
            const snapshot = await this.resnapshotCallback(context);
            await reached;
            this.snapshot = snapshot;
            this.resnapshotState = undefined;
            for (const held of resnapshotState.held)
                this.push(held.event, held.context);
            return snapshot;
        }
        catch (error) {
            this.resnapshotState = undefined;
            for (const held of resnapshotState.held)
                this.push(held.event, held.context);
            throw error;
        }
    }
    markResnapshotBoundary() {
        const resnapshot = this.resnapshotState;
        if (resnapshot === undefined || resnapshot.phase !== "dropping")
            return;
        resnapshot.phase = "holding";
        resnapshot.resolveReached();
    }
    unsubscribe() {
        if (this.state === "unsubscribed")
            return;
        this.state = "unsubscribed";
        this.buffer = [];
        this.listener = undefined;
        this.unsubscribeCallback?.();
        this.unsubscribeCallback = undefined;
    }
    push(event, context) {
        if (this.state === "unsubscribed")
            return;
        if (this.resnapshotState?.phase === "dropping")
            return;
        if (this.resnapshotState?.phase === "holding") {
            this.resnapshotState.held.push({ event, context });
            return;
        }
        if (this.state === "buffering") {
            this.buffer.push({ event, context, epoch: this.epoch });
            return;
        }
        this.enqueue(event, context, this.epoch);
    }
    setUnsubscribe(callback) {
        this.unsubscribeCallback = callback;
    }
    enqueue(event, context, epoch) {
        const listener = this.listener;
        if (listener === undefined)
            return;
        this.deliveryTail = this.deliveryTail
            .then(async () => {
            if (this.state === "started" && epoch === this.epoch)
                await listener(event, context);
        })
            .catch(async (error) => {
            try {
                await this.onError(error, event, context);
            }
            catch { }
        });
    }
}
//# sourceMappingURL=events.js.map