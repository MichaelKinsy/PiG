import { getTelemetryContext } from "../context.js";
import { AbortRequested } from "./effect-gate.js";
function createRequestOptions(config, captureMetadata, context) {
    const options = config.streamOptions;
    return {
        transport: options.transport,
        timeoutMs: options.timeoutMs,
        maxRetries: options.maxRetries,
        maxRetryDelayMs: options.maxRetryDelayMs,
        headers: options.headers,
        metadata: options.metadata,
        cacheRetention: options.cacheRetention,
        deferred: options.deferred,
        ...(config.thinkingLevel === "off" ? {} : { reasoning: config.thinkingLevel }),
        signal: context.abortSignal,
        telemetryContext: getTelemetryContext(context),
        onPayload: config.beforePayload === undefined
            ? undefined
            : (payload, model) => config.beforePayload?.(payload, model, context),
        onResponse: (response) => {
            captureMetadata({ status: response.status, headers: response.headers });
        },
    };
}
function isUpdateEvent(event) {
    return event.type !== "start" && event.type !== "done" && event.type !== "error";
}
export async function consumeAssistantStream(stream, observer, afterResponse, context) {
    let started = false;
    for await (const event of stream) {
        if (event.type === "start") {
            if (started)
                throw new Error("Assistant message stream emitted more than one start event");
            started = true;
            await observer.start({ ...event.partial }, event, context);
        }
        else if (isUpdateEvent(event)) {
            if (!started)
                throw new Error(`Assistant message stream emitted ${event.type} before start`);
            await observer.update({ ...event.partial }, event, context);
        }
        else if (event.type === "done" && !started) {
            throw new Error("Assistant message stream emitted done before start");
        }
    }
    const settled = (await stream.result());
    let finalMessage = settled;
    if (afterResponse !== undefined) {
        try {
            finalMessage = await afterResponse(settled, context);
        }
        catch (error) {
            if (!(error instanceof AbortRequested))
                throw error;
            await error.cancellation;
        }
    }
    await observer.end(finalMessage, context);
    return finalMessage;
}
/** Stream one assistant response without mutating the caller's message list. */
export async function streamHarnessAssistant(messages, config, context) {
    let requestContext = { messages: messages.slice(), systemPrompt: config.systemPrompt };
    if (config.transformContext) {
        requestContext = await config.transformContext(requestContext, context);
    }
    const providerMessages = await config.toProviderMessages(requestContext.messages, context);
    const aiContext = {
        systemPrompt: requestContext.systemPrompt,
        messages: providerMessages,
        tools: config.tools,
    };
    let metadata = {};
    const stream = await config.request(aiContext, createRequestOptions(config, (nextMetadata) => {
        metadata = nextMetadata;
    }, context), context);
    const afterResponse = config.afterResponse;
    return consumeAssistantStream(stream, config.observer, afterResponse === undefined
        ? undefined
        : (message, afterContext) => afterResponse(message, metadata, afterContext), context);
}
//# sourceMappingURL=assistant.js.map