async function startNoopSpan(_options, callback) {
    return callback(noopTelemetrySpan);
}
const noopCounter = Object.freeze({ add: () => { } });
const noopUpDownCounter = Object.freeze({ add: () => { } });
const noopHistogram = Object.freeze({ record: () => { } });
/** Shared instruments that never inspect or retain metadata, measurements, or contexts. */
export const NOOP_TELEMETRY_METRICS = Object.freeze({
    counter: () => noopCounter,
    upDownCounter: () => noopUpDownCounter,
    histogram: () => noopHistogram,
});
/** Shared propagation capability that neither inspects nor retains its inputs. */
export const NOOP_TELEMETRY_PROPAGATION = Object.freeze({
    inject: () => undefined,
    extract: () => NOOP_TELEMETRY_CONTEXT,
});
const noopTelemetrySpan = {
    metrics: NOOP_TELEMETRY_METRICS,
    propagation: NOOP_TELEMETRY_PROPAGATION,
    startSpan: startNoopSpan,
    addLink: () => { },
    addEvent: () => { },
    setAttributes: () => { },
    setStatus: () => { },
};
Object.freeze(noopTelemetrySpan);
/** Shared telemetry context used when an application does not provide one. */
export const NOOP_TELEMETRY_CONTEXT = noopTelemetrySpan;
//# sourceMappingURL=noop.js.map