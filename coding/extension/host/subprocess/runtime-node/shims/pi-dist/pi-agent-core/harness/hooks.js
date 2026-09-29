import { withAbortSignal } from "./context.js";
import { startHarnessSpan } from "./telemetry.js";
/** Ordered harness hook registry and aggregate runner. */
export class HookRegistry {
    registrations = new Map();
    reportError;
    closedError;
    constructor(reportError) {
        this.reportError = reportError;
    }
    on(name, handler, options = {}) {
        if (this.closedError !== undefined)
            throw this.closedError;
        const registrations = this.registrations.get(name) ?? [];
        const registration = {
            ...(options.id === undefined ? {} : { id: options.id }),
            handler: (event, context) => handler(event, context),
        };
        registrations.push(registration);
        this.registrations.set(name, registrations);
        return () => {
            const index = registrations.indexOf(registration);
            if (index !== -1)
                registrations.splice(index, 1);
        };
    }
    has(name) {
        return (this.registrations.get(name)?.length ?? 0) !== 0;
    }
    /** Invoke one accepted-operation aggregate after synchronously passing its effect gate. */
    runWithGate(name, event, gate, context) {
        return gate.admit(() => {
            const admittedContext = withAbortSignal(gate.signal, context);
            admittedContext.abortSignal?.throwIfAborted();
            return this.runAdmitted(name, event, admittedContext);
        });
    }
    /** Invoke a tool-hook aggregate with one telemetry span per registered handler. */
    runToolWithGate(name, event, gate, context) {
        return gate.admit(() => {
            const admittedContext = withAbortSignal(gate.signal, context);
            admittedContext.abortSignal?.throwIfAborted();
            return (name === "before_tool"
                ? this.beforeTool(event, admittedContext)
                : this.afterTool(event, admittedContext));
        });
    }
    close(error) {
        this.closedError ??= error;
    }
    async runAdmitted(name, event, context) {
        if (this.closedError !== undefined)
            throw this.closedError;
        const result = await this.aggregate(name, event, context);
        return result;
    }
    async aggregate(name, event, context) {
        switch (name) {
            case "before_run":
                return this.beforeRun(event, context);
            case "before_drive":
                await this.invokeAllFailClosed(name, event, context);
                return undefined;
            case "before_run_end": {
                let followUp;
                await this.invokeAll(name, event, (value) => {
                    const result = value;
                    if (result?.followUp !== undefined)
                        followUp = result.followUp;
                }, context);
                return followUp === undefined ? undefined : { followUp };
            }
            case "transform_context":
                return this.transformContext(event, context);
            case "before_request":
                return this.beforeRequest(event, context);
            case "before_payload":
                return this.beforePayload(event, context);
            case "after_response":
                return this.afterResponse(event, context);
            case "before_tool":
                return this.beforeTool(event, context);
            case "after_tool":
                return this.afterTool(event, context);
            case "before_compaction":
                return this.firstStructural(name, event, "compaction", context);
            case "before_navigation":
                return this.firstStructural(name, event, "summary", context);
        }
    }
    async beforeRun(event, context) {
        let prompt = event.prompt;
        let injected = [];
        for (const registration of this.registrationsFor("before_run")) {
            try {
                const result = (await registration.handler({ ...event, prompt }, context));
                if (result?.messages !== undefined) {
                    injected = [...injected, ...result.messages];
                    prompt = [...prompt, ...result.messages];
                }
            }
            catch (error) {
                await this.reportError(error instanceof Error ? error : new Error(String(error)), "before_run", event.lane, context);
            }
        }
        return injected.length === 0 ? undefined : { messages: injected };
    }
    async beforeTool(event, context) {
        let args = event.args;
        let block;
        for (const registration of this.registrationsFor("before_tool")) {
            try {
                const result = (await this.invokeToolRegistration("before_tool", registration, { ...event, args }, context));
                if (result?.args !== undefined)
                    args = result.args;
                if (result?.block !== undefined) {
                    block = result.block;
                    break;
                }
            }
            catch (error) {
                const normalized = error instanceof Error ? error : new Error(String(error));
                await this.reportError(normalized, "before_tool", event.lane, context);
                block = { reason: normalized.message };
                break;
            }
        }
        return {
            ...(args === event.args ? {} : { args }),
            ...(block === undefined ? {} : { block }),
        };
    }
    async transformContext(event, context) {
        let messages = event.messages;
        let systemPrompt = event.systemPrompt;
        for (const registration of this.registrationsFor("transform_context")) {
            try {
                const result = (await registration.handler({
                    ...event,
                    messages,
                    systemPrompt,
                }, context));
                if (result?.messages !== undefined)
                    messages = result.messages;
                if (result?.systemPrompt !== undefined)
                    systemPrompt = result.systemPrompt;
            }
            catch (error) {
                await this.reportError(error instanceof Error ? error : new Error(String(error)), "transform_context", event.lane, context);
            }
        }
        return { messages, systemPrompt };
    }
    async beforeRequest(event, context) {
        let streamOptions = event.streamOptions;
        let changed = false;
        for (const registration of this.registrationsFor("before_request")) {
            try {
                const result = (await registration.handler({ ...event, streamOptions }, context));
                if (result?.streamOptions !== undefined) {
                    streamOptions = applyStreamOptionsPatch(streamOptions, result.streamOptions);
                    changed = true;
                }
            }
            catch (error) {
                await this.reportError(error instanceof Error ? error : new Error(String(error)), "before_request", event.lane, context);
            }
        }
        return changed ? { streamOptions: createStreamOptionsPatch(event.streamOptions, streamOptions) } : undefined;
    }
    async beforePayload(event, context) {
        let payload = event.payload;
        for (const registration of this.registrationsFor("before_payload")) {
            try {
                const result = (await registration.handler({ ...event, payload }, context));
                if (result?.payload !== undefined)
                    payload = result.payload;
            }
            catch (error) {
                await this.reportError(error instanceof Error ? error : new Error(String(error)), "before_payload", event.lane, context);
            }
        }
        return { payload };
    }
    async afterResponse(event, context) {
        let message = event.message;
        for (const registration of this.registrationsFor("after_response")) {
            try {
                const result = (await registration.handler({ ...event, message }, context));
                if (result?.message !== undefined)
                    message = result.message;
            }
            catch (error) {
                await this.reportError(error instanceof Error ? error : new Error(String(error)), "after_response", event.lane, context);
            }
        }
        return { message };
    }
    async afterTool(event, context) {
        let current = {
            content: event.content,
            details: event.details,
            isError: event.isError,
            usage: event.usage,
        };
        const aggregate = {};
        for (const registration of this.registrationsFor("after_tool")) {
            try {
                const result = (await this.invokeToolRegistration("after_tool", registration, { ...event, ...current }, context));
                if (result === undefined)
                    continue;
                if (result.content !== undefined)
                    aggregate.content = result.content;
                if (result.details !== undefined)
                    aggregate.details = result.details;
                if (result.isError !== undefined)
                    aggregate.isError = result.isError;
                if (result.usage !== undefined)
                    aggregate.usage = result.usage;
                if (result.terminate !== undefined)
                    aggregate.terminate = result.terminate;
                current = {
                    content: result.content === undefined ? current.content : result.content,
                    details: result.details === undefined ? current.details : result.details,
                    isError: result.isError === undefined ? current.isError : result.isError,
                    usage: result.usage === undefined ? current.usage : result.usage,
                };
            }
            catch (error) {
                await this.reportError(error instanceof Error ? error : new Error(String(error)), "after_tool", event.lane, context);
            }
        }
        return Object.keys(aggregate).length === 0 ? undefined : aggregate;
    }
    async firstStructural(name, event, resultField, context) {
        for (const registration of this.registrationsFor(name)) {
            try {
                const value = await registration.handler(event, context);
                if (value === undefined || value === null || typeof value !== "object")
                    continue;
                const result = value;
                if (result.decline === true && result[resultField] !== undefined) {
                    await this.reportError(new Error(`${name} hook cannot return both decline and ${resultField}`), name, event.lane, context);
                    continue;
                }
                if (result.decline === true || result[resultField] !== undefined)
                    return value;
            }
            catch (error) {
                await this.reportError(error instanceof Error ? error : new Error(String(error)), name, event.lane, context);
            }
        }
        return undefined;
    }
    invokeToolRegistration(name, registration, event, context) {
        return startHarnessSpan("pi.harness.hook", {
            "pi.lane.name": event.lane,
            "pi.operation.id": event.runId,
            "pi.hook.name": name,
            ...(registration.id === undefined ? {} : { "pi.hook.registration_id": registration.id }),
        }, async (span, spanContext) => {
            try {
                const result = await registration.handler(event, spanContext);
                const blocked = name === "before_tool" &&
                    result !== null &&
                    typeof result === "object" &&
                    "block" in result &&
                    result.block !== undefined;
                span.setAttributes({ "pi.hook.outcome": blocked ? "blocked" : "completed" });
                return result;
            }
            catch (error) {
                span.setAttributes({ "pi.hook.outcome": "failed" });
                span.setStatus({ status: "error" });
                throw error;
            }
        }, context);
    }
    registrationsFor(name) {
        return [...(this.registrations.get(name) ?? [])];
    }
    async invokeAllFailClosed(name, event, context) {
        for (const registration of this.registrationsFor(name)) {
            try {
                await registration.handler(event, context);
            }
            catch (error) {
                const normalized = error instanceof Error ? error : new Error(String(error));
                await this.reportError(normalized, name, event.lane, context);
                throw normalized;
            }
        }
    }
    async invokeAll(name, event, apply, context) {
        for (const registration of this.registrationsFor(name)) {
            try {
                apply(await registration.handler(event, context));
            }
            catch (error) {
                await this.reportError(error instanceof Error ? error : new Error(String(error)), name, event.lane, context);
            }
        }
    }
}
export function applyStreamOptionsPatch(base, patch) {
    const next = { ...base };
    for (const key of [
        "transport",
        "timeoutMs",
        "maxRetries",
        "maxRetryDelayMs",
        "cacheRetention",
        "deferred",
    ]) {
        if (!(key in patch))
            continue;
        const value = patch[key];
        if (value === undefined)
            delete next[key];
        else
            Object.assign(next, { [key]: value });
    }
    if ("headers" in patch) {
        if (patch.headers === undefined)
            delete next.headers;
        else {
            const headers = { ...next.headers };
            for (const [key, value] of Object.entries(patch.headers)) {
                if (value === undefined)
                    delete headers[key];
                else
                    headers[key] = value;
            }
            next.headers = headers;
        }
    }
    if ("metadata" in patch) {
        if (patch.metadata === undefined)
            delete next.metadata;
        else {
            const metadata = { ...next.metadata };
            for (const [key, value] of Object.entries(patch.metadata)) {
                if (value === undefined)
                    delete metadata[key];
                else
                    metadata[key] = value;
            }
            next.metadata = metadata;
        }
    }
    return next;
}
function createStreamOptionsPatch(base, value) {
    const patch = {};
    for (const key of [
        "transport",
        "timeoutMs",
        "maxRetries",
        "maxRetryDelayMs",
        "cacheRetention",
        "deferred",
    ]) {
        if (base[key] !== value[key])
            Object.assign(patch, { [key]: value[key] });
    }
    if (base.headers !== value.headers) {
        if (value.headers === undefined)
            patch.headers = undefined;
        else {
            const headers = {};
            for (const key of Object.keys(base.headers ?? {})) {
                if (!(key in value.headers))
                    headers[key] = undefined;
            }
            for (const [key, header] of Object.entries(value.headers)) {
                if (base.headers?.[key] !== header)
                    headers[key] = header;
            }
            if (base.headers === undefined && Object.keys(headers).length === 0)
                patch.headers = {};
            else if (Object.keys(headers).length !== 0)
                patch.headers = headers;
        }
    }
    if (base.metadata !== value.metadata) {
        if (value.metadata === undefined)
            patch.metadata = undefined;
        else {
            const metadata = {};
            for (const key of Object.keys(base.metadata ?? {})) {
                if (!(key in value.metadata))
                    metadata[key] = undefined;
            }
            for (const [key, metadataValue] of Object.entries(value.metadata)) {
                if (base.metadata?.[key] !== metadataValue)
                    metadata[key] = metadataValue;
            }
            if (base.metadata === undefined && Object.keys(metadata).length === 0)
                patch.metadata = {};
            else if (Object.keys(metadata).length !== 0)
                patch.metadata = metadata;
        }
    }
    return patch;
}
//# sourceMappingURL=hooks.js.map