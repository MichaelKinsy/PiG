const sources = new WeakMap();
export function registerReplicatedStateInternals(value, internals) {
    sources.set(value, internals);
}
export function getReplicatedStateInternals(value) {
    if (typeof value !== "object" || value === null)
        return undefined;
    return sources.get(value);
}
//# sourceMappingURL=state-internals.js.map