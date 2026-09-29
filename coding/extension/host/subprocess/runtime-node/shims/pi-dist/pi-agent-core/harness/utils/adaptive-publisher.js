/**
 * Publishes the latest state without queuing intermediate mutations.
 *
 * The first dirty state after idle is immediate. Each publication then buys a
 * delay proportional to its encoded size, with a minimum interval that also
 * bounds event count. A single trailing timer guarantees eventual publication.
 */
export class AdaptivePublisher {
    #options;
    #minIntervalMs;
    #targetBytesPerSecond;
    #published;
    #dirty = false;
    #nextEmitAt = 0;
    #timer;
    #disposed = false;
    constructor(options) {
        this.#options = options;
        this.#minIntervalMs = options.minIntervalMs ?? 100;
        this.#targetBytesPerSecond = options.targetBytesPerSecond ?? 100 * 1024;
    }
    markDirty() {
        if (this.#disposed)
            return;
        this.#dirty = true;
        const wait = this.#nextEmitAt - Date.now();
        if (wait <= 0) {
            this.flush();
            return;
        }
        this.#armTimer(wait);
    }
    flush(force = false) {
        if (this.#disposed || !this.#dirty)
            return;
        const now = Date.now();
        if (!force && now < this.#nextEmitAt) {
            this.#armTimer(this.#nextEmitAt - now);
            return;
        }
        if (this.#timer !== undefined)
            clearTimeout(this.#timer);
        this.#timer = undefined;
        const current = this.#options.snapshot();
        const update = this.#options.update(this.#published, current);
        if (update === undefined) {
            this.#published = current;
            this.#dirty = false;
            return;
        }
        const encodedBytes = this.#options.measure(update);
        this.#published = current;
        this.#dirty = false;
        this.#nextEmitAt = now + Math.max(this.#minIntervalMs, (encodedBytes * 1000) / this.#targetBytesPerSecond);
        // Commit before delivery. A consumer may apply the update and then throw or
        // reenter the producer; retaining the old baseline would duplicate that delta.
        this.#options.publish(update);
    }
    dispose() {
        if (this.#timer !== undefined)
            clearTimeout(this.#timer);
        this.#timer = undefined;
        this.#disposed = true;
    }
    #armTimer(wait) {
        if (this.#timer !== undefined)
            return;
        this.#timer = setTimeout(() => {
            this.#timer = undefined;
            try {
                this.flush();
            }
            catch (error) {
                this.#options.onError(error);
            }
        }, wait);
    }
}
//# sourceMappingURL=adaptive-publisher.js.map