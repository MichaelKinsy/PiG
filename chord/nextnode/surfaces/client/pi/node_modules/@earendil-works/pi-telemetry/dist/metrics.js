/** Bind instrument names, kinds, metadata, and exact measurement attributes to one or more schemas. */
export function createTypedMetrics(metrics, schemas) {
    const definitions = new Map();
    for (const schema of schemas) {
        for (const [name, definition] of Object.entries(schema.metrics ?? {}))
            definitions.set(name, definition);
    }
    return {
        counter: (name) => metrics.counter(name, definitions.get(name)),
        upDownCounter: (name) => metrics.upDownCounter(name, definitions.get(name)),
        histogram: (name) => metrics.histogram(name, definitions.get(name)),
    };
}
//# sourceMappingURL=metrics.js.map