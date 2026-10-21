import { NOOP_TELEMETRY_CONTEXT } from "./noop.js";
function carrierId(carrier) {
    return `${carrier["pi-memory-scope"]}:${carrier["pi-memory-span"]}`;
}
// Identity survives settlement without exposing backend IDs on the generic context contract.
const contextCarriers = new WeakMap();
function copyAttributes(attributes) {
    return Object.fromEntries(Object.entries(attributes ?? {})
        .filter(([, value]) => value !== undefined)
        .map(([name, value]) => [name, Array.isArray(value) ? [...value] : value]));
}
function copyLink(link) {
    const context = contextCarriers.get(link.context);
    return context ? { context: { ...context }, attributes: copyAttributes(link.attributes) } : undefined;
}
function copyStatus(status) {
    if (status.status === "ok")
        return { status: "ok" };
    return status.error
        ? { status: "error", error: { name: status.error.name, message: status.error.message } }
        : { status: "error" };
}
function createSpan(state, parent, options) {
    const name = options.name;
    const attributes = copyAttributes(options.attributes);
    const links = [];
    for (const link of options.links ?? []) {
        const copy = copyLink(link);
        if (copy)
            links.push(copy);
    }
    const id = state.nextSpanId++;
    return {
        startedMs: Date.now(),
        clock: performance.now(),
        id,
        parentId: parent?.localId ?? null,
        context: {
            "pi-memory-scope": state.scope,
            "pi-memory-span": String(id),
            "pi-memory-trace": parent?.context["pi-memory-trace"] ?? `${state.scope}:${id}`,
        },
        parentContext: parent ? { ...parent.context } : null,
        links,
        name,
        attributes,
        events: [],
        status: { status: "ok" },
        explicitStatus: false,
        settled: false,
    };
}
function startInertSpan(state, options, callback) {
    return NOOP_TELEMETRY_CONTEXT.startSpan(options, (noop) => callback({
        ...noop,
        metrics: state.metrics,
        propagation: state.propagation,
        startSpan: (childOptions, childCallback) => startInertSpan(state, childOptions, childCallback),
    }));
}
async function startInMemorySpan(state, parent, options, callback) {
    if (parent?.span?.settled)
        return startInertSpan(state, options, callback);
    const recordedSpan = createSpan(state, parent, options);
    state.spans.push(recordedSpan);
    const span = {
        metrics: state.metrics,
        propagation: state.propagation,
        startSpan: (childOptions, childCallback) => startInMemorySpan(state, { context: recordedSpan.context, localId: recordedSpan.id, span: recordedSpan }, childOptions, childCallback),
        addLink(link) {
            if (recordedSpan.settled)
                return;
            const copy = copyLink(link);
            if (copy)
                recordedSpan.links.push(copy);
        },
        addEvent(name, attributes) {
            if (recordedSpan.settled)
                return;
            recordedSpan.events.push({
                name,
                timeMs: performance.now() - recordedSpan.clock,
                attributes: copyAttributes(attributes),
            });
        },
        setAttributes(attributes) {
            if (recordedSpan.settled)
                return;
            recordedSpan.attributes = { ...recordedSpan.attributes, ...copyAttributes(attributes) };
        },
        setStatus(status) {
            if (recordedSpan.settled)
                return;
            recordedSpan.status = copyStatus(status);
            recordedSpan.explicitStatus = true;
        },
    };
    contextCarriers.set(span, recordedSpan.context);
    try {
        return await callback(span);
    }
    catch (error) {
        if (!recordedSpan.explicitStatus)
            recordedSpan.status =
                error instanceof Error
                    ? { status: "error", error: { name: error.name, message: error.message } }
                    : { status: "error" };
        throw error;
    }
    finally {
        recordedSpan.settled = true;
        recordedSpan.endedMs = recordedSpan.startedMs + performance.now() - recordedSpan.clock;
        recordedSpan.endSequence = state.nextEndSequence++;
    }
}
function createMeasurementRecorder(state, kind, name, options) {
    const description = options?.description;
    const unit = options?.unit;
    const boundaries = kind === "histogram" && options?.boundaries !== undefined ? [...options.boundaries] : undefined;
    const metadata = {
        kind,
        name,
        ...(description === undefined ? {} : { description }),
        ...(unit === undefined ? {} : { unit }),
        ...(boundaries === undefined ? {} : { boundaries }),
    };
    return (value, attributes, context) => {
        const carrier = context === undefined ? undefined : contextCarriers.get(context);
        state.measurements.push({
            intervalMs: Math.floor(Date.now() / 10_000) * 10_000,
            measurement: {
                ...metadata,
                value,
                attributes: copyAttributes(attributes),
                context: carrier ? { ...carrier } : null,
            },
        });
    };
}
/**
 * Backend-neutral reference implementation that records spans in process memory.
 * Create a fresh instance to isolate tests or independent recording scopes.
 */
export class InMemoryTelemetryContext {
    metrics = this;
    propagation = this;
    state = {
        metrics: this,
        propagation: this,
        scope: crypto.randomUUID(),
        spans: [],
        measurements: [],
        nextSpanId: 1,
        nextEndSequence: 1,
    };
    startSpan(options, callback) {
        return startInMemorySpan(this.state, undefined, options, callback);
    }
    /** Returns a detached carrier for a known span or extracted parent, including settled spans. */
    inject(context) {
        const carrier = contextCarriers.get(context);
        return carrier ? { ...carrier } : undefined;
    }
    /** Missing or non-string carrier identities fall back to this recorder's root context. */
    extract(carrier) {
        const scope = carrier?.["pi-memory-scope"];
        const span = carrier?.["pi-memory-span"];
        const traceId = carrier?.["pi-memory-trace"];
        if (typeof scope !== "string" || typeof span !== "string" || typeof traceId !== "string")
            return this;
        const context = { "pi-memory-scope": scope, "pi-memory-span": span, "pi-memory-trace": traceId };
        const localId = scope === this.state.scope ? Number(span) : null;
        const parent = {
            metrics: this,
            propagation: this,
            startSpan: (options, callback) => startInMemorySpan(this.state, { context, localId }, options, callback),
        };
        contextCarriers.set(parent, context);
        return parent;
    }
    counter(name, options) {
        return { add: createMeasurementRecorder(this.state, "counter", name, options) };
    }
    upDownCounter(name, options) {
        return { add: createMeasurementRecorder(this.state, "upDownCounter", name, options) };
    }
    histogram(name, options) {
        return { record: createMeasurementRecorder(this.state, "histogram", name, options) };
    }
    storedSpans() {
        return this.state.spans
            .filter((span) => span.settled)
            .map((span) => ({
            id: carrierId(span.context),
            traceId: span.context["pi-memory-trace"],
            ...(span.parentContext ? { parentId: carrierId(span.parentContext) } : {}),
            name: span.name,
            startedMs: span.startedMs,
            endedMs: span.endedMs,
            attributes: copyAttributes(span.attributes),
            events: span.events.map((event) => ({
                name: event.name,
                timeMs: event.timeMs,
                attributes: copyAttributes(event.attributes),
            })),
            links: span.links.map((link) => ({
                traceId: link.context["pi-memory-trace"],
                spanId: carrierId(link.context),
                attributes: copyAttributes(link.attributes),
            })),
            status: copyStatus(span.status),
        }))
            .sort((a, b) => a.startedMs - b.startedMs || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
    }
    async getTrace(traceId) {
        const spans = this.storedSpans().filter((span) => span.traceId === traceId);
        return spans.length ? { traceId, spans } : undefined;
    }
    async findTraces(query = {}) {
        const limit = query.limit ?? 20;
        const after = query.cursor === undefined ? undefined : JSON.parse(query.cursor);
        const grouped = new Map();
        for (const span of this.storedSpans()) {
            let trace = grouped.get(span.traceId);
            if (!trace) {
                trace = { traceId: span.traceId, spans: [] };
                grouped.set(span.traceId, trace);
            }
            trace.spans.push(span);
        }
        const matches = [...grouped.values()]
            .filter((trace) => {
            const first = trace.spans[0];
            return ((query.name === undefined || first.name === query.name) &&
                (query.startMin === undefined || first.startedMs >= query.startMin) &&
                (query.startMax === undefined || first.startedMs <= query.startMax) &&
                (!after || first.startedMs < after[0] || (first.startedMs === after[0] && trace.traceId > after[1])) &&
                (!query.errors || trace.spans.some((span) => span.status.status === "error")) &&
                (!query.attributes ||
                    trace.spans.some((span) => Object.entries(query.attributes).every(([key, value]) => span.attributes[key] === value))));
        })
            .sort((a, b) => b.spans[0].startedMs - a.spans[0].startedMs ||
            (a.traceId < b.traceId ? -1 : a.traceId > b.traceId ? 1 : 0));
        const traces = matches.slice(0, limit);
        const last = traces.at(-1);
        return {
            traces,
            ...(matches.length > limit ? { cursor: JSON.stringify([last.spans[0].startedMs, last.traceId]) } : {}),
        };
    }
    /** Read raw measurements as count-one rows, without changing getMetrics() snapshots. */
    async findMetrics(query) {
        return this.state.measurements
            .filter(({ intervalMs, measurement }) => measurement.name === query.name &&
            (query.startMin === undefined || intervalMs >= query.startMin) &&
            (query.startMax === undefined || intervalMs <= query.startMax) &&
            Object.entries(query.attributes ?? {}).every(([key, value]) => measurement.attributes[key] === value))
            .map(({ intervalMs, measurement }) => ({
            intervalMs,
            name: measurement.name,
            kind: measurement.kind,
            ...(measurement.unit === undefined ? {} : { unit: measurement.unit }),
            attributes: copyAttributes(measurement.attributes),
            count: 1,
            sum: measurement.value,
            min: measurement.value,
            max: measurement.value,
        }));
    }
    /** Returns detached measurements in recording order, without aggregation. */
    getMetrics() {
        return this.state.measurements.map(({ measurement }) => ({
            ...measurement,
            ...(measurement.boundaries === undefined ? {} : { boundaries: [...measurement.boundaries] }),
            attributes: copyAttributes(measurement.attributes),
            context: measurement.context ? { ...measurement.context } : null,
        }));
    }
    /** Returns detached snapshots in span-start order. */
    getSpans() {
        return this.state.spans.map((span) => ({
            id: span.id,
            parentId: span.parentId,
            context: { ...span.context },
            parentContext: span.parentContext ? { ...span.parentContext } : null,
            links: span.links.map((link) => ({
                context: { ...link.context },
                attributes: copyAttributes(link.attributes),
            })),
            name: span.name,
            attributes: copyAttributes(span.attributes),
            events: span.events.map((event) => ({
                name: event.name,
                attributes: copyAttributes(event.attributes),
            })),
            status: copyStatus(span.status),
            settled: span.settled,
            ...(span.endSequence === undefined ? {} : { endSequence: span.endSequence }),
        }));
    }
}
//# sourceMappingURL=memory.js.map