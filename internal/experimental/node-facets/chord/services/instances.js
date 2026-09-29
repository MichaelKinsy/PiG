import { BACKGROUND_CONTEXT, withCancel } from "../context/index.js";
/** Owns keyed instance lifetime and the cancellable tasks observing those instances. */
export class InstanceDirectory {
    #entries = new Map();
    #observers = new Set();
    #reportError;
    #ready;
    #disposed = false;
    constructor(options) {
        this.#ready = options.ready;
        this.#reportError = options.onError;
    }
    get observerCount() {
        return this.#observers.size;
    }
    get(key) {
        return this.#entries.get(key);
    }
    insert(entry) {
        this.#assertActive();
        if (this.#entries.has(entry.key)) {
            throw new Error(`Keyed service already has a live instance with key ${entry.key}`);
        }
        this.#entries.set(entry.key, entry);
        if (this.#ready)
            this.#startAll(entry);
    }
    replace(entry) {
        this.#assertActive();
        const previous = this.#entries.get(entry.key);
        if (previous !== undefined) {
            if (previous.generation === entry.generation) {
                throw new Error("Keyed service repeated a live generation");
            }
            this.#remove(previous);
        }
        this.#entries.set(entry.key, entry);
        if (this.#ready)
            this.#startAll(entry);
    }
    remove(entry) {
        if (this.#entries.get(entry.key) !== entry)
            return;
        this.#remove(entry);
    }
    ready() {
        this.#assertActive();
        if (this.#ready)
            return;
        this.#ready = true;
        for (const entry of this.#entries.values())
            this.#startAll(entry);
    }
    reset() {
        if (this.#disposed)
            return;
        this.#ready = false;
        for (const entry of [...this.#entries.values()])
            this.#remove(entry);
    }
    observe(handler) {
        this.#assertActive();
        const observer = {
            handler: handler,
            tasks: new Map(),
            closed: false,
        };
        this.#observers.add(observer);
        if (this.#ready) {
            for (const entry of this.#entries.values())
                this.#start(observer, entry);
        }
        return () => {
            if (observer.closed)
                return;
            observer.closed = true;
            for (const task of observer.tasks.values())
                task.cancel();
            observer.tasks.clear();
            this.#observers.delete(observer);
        };
    }
    dispose() {
        if (this.#disposed)
            return;
        this.#disposed = true;
        for (const observer of this.#observers) {
            observer.closed = true;
            for (const task of observer.tasks.values())
                task.cancel();
            observer.tasks.clear();
        }
        this.#observers.clear();
        for (const entry of [...this.#entries.values()]) {
            entry.deactivate();
        }
        this.#entries.clear();
    }
    #remove(entry) {
        if (this.#entries.get(entry.key) !== entry)
            return;
        this.#entries.delete(entry.key);
        entry.deactivate();
        for (const observer of this.#observers) {
            observer.tasks.get(entry)?.cancel();
            observer.tasks.delete(entry);
        }
    }
    #startAll(entry) {
        for (const observer of this.#observers)
            this.#start(observer, entry);
    }
    #start(observer, entry) {
        if (observer.closed || observer.tasks.has(entry))
            return;
        const { context, cancel } = withCancel(BACKGROUND_CONTEXT);
        observer.tasks.set(entry, { cancel });
        try {
            void Promise.resolve(observer.handler(entry.service, context)).catch((error) => {
                if (!context.abortSignal?.aborted)
                    this.#reportError(toError(error));
            });
        }
        catch (error) {
            if (!context.abortSignal?.aborted)
                this.#reportError(toError(error));
        }
    }
    #assertActive() {
        if (this.#disposed)
            throw new Error("Keyed service directory is disposed");
    }
}
function toError(error) {
    return error instanceof Error ? error : new Error(String(error));
}
//# sourceMappingURL=instances.js.map