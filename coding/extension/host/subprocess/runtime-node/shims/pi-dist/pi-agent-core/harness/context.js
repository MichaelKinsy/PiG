import { awaitWithContext, BACKGROUND_CONTEXT, createContextKey, TODO_CONTEXT, withAbortSignal, withCancel, withContextValue, withoutAbortSignal, } from "../../chord/context/index.js";
import { NOOP_TELEMETRY_CONTEXT } from "../../pi-telemetry/index.js";
export { awaitWithContext, BACKGROUND_CONTEXT, createContextKey, TODO_CONTEXT, withAbortSignal, withCancel, withContextValue, withoutAbortSignal, };
const TELEMETRY_CONTEXT_KEY = createContextKey("pi.telemetryContext");
/** Return the telemetry parent attached to a context, or the shared no-op parent. */
export function getTelemetryContext(context) {
    return context.value(TELEMETRY_CONTEXT_KEY) ?? NOOP_TELEMETRY_CONTEXT;
}
/** Derive a context whose telemetry children use the supplied parent or active span. */
export function withTelemetryContext(telemetryContext, context) {
    return withContextValue(TELEMETRY_CONTEXT_KEY, telemetryContext, context);
}
//# sourceMappingURL=context.js.map