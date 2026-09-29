/** Async credential store overlay for non-persistent runtime API keys. */
export class RuntimeCredentials {
    store;
    overrides = new Map();
    constructor(store) {
        this.store = store;
    }
    setRuntimeApiKey(providerId, apiKey) {
        this.overrides.set(providerId, apiKey);
    }
    removeRuntimeApiKey(providerId) {
        this.overrides.delete(providerId);
    }
    hasRuntimeApiKey(providerId) {
        return this.overrides.has(providerId);
    }
    async read(providerId, options) {
        options?.signal?.throwIfAborted();
        const override = this.overrides.get(providerId);
        return override ? { type: "api_key", key: override } : this.store.read(providerId, options);
    }
    async list(options) {
        const entries = new Map((await this.store.list(options)).map((entry) => [entry.providerId, entry]));
        options?.signal?.throwIfAborted();
        for (const providerId of this.overrides.keys()) {
            entries.set(providerId, { providerId, type: "api_key" });
        }
        return [...entries.values()];
    }
    modify(providerId, fn, options) {
        return this.store.modify(providerId, fn, options);
    }
    async delete(providerId, options) {
        options?.signal?.throwIfAborted();
        await this.store.delete(providerId, options);
        this.overrides.delete(providerId);
    }
}
//# sourceMappingURL=runtime-credentials.js.map