import { validateToolArguments } from "../../../pi-ai/sdk-bundle/index.js";
import { withAbortSignal } from "../context.js";
function createErrorToolResult(message) {
    return {
        content: [{ type: "text", text: message }],
        details: undefined,
    };
}
function immediateError(toolCall, message, terminate = false) {
    return {
        kind: "immediate",
        toolCall,
        result: createErrorToolResult(message),
        isError: true,
        terminate,
    };
}
/** Resolve a tool, apply its deterministic argument preparation, and validate the result. */
export function prepareToolCall(call, tools) {
    const tool = tools.find((candidate) => candidate.name === call.name);
    if (!tool) {
        return immediateError(call, `Tool ${JSON.stringify(call.name)} is unavailable`);
    }
    try {
        const preparedArguments = tool.prepareArguments ? tool.prepareArguments(call.arguments) : call.arguments;
        const preparedCall = preparedArguments === call.arguments
            ? call
            : { ...call, arguments: preparedArguments };
        const args = validateToolArguments(tool, preparedCall);
        return { toolCall: call, tool, args };
    }
    catch (error) {
        return immediateError(call, error instanceof Error ? error.message : String(error));
    }
}
/** Apply an explicit hook decision and revalidate replacement arguments. */
export function applyBeforeToolDecision(prepared, decision) {
    if (decision?.block) {
        return immediateError(prepared.toolCall, decision.block.reason, decision.block.terminate === true);
    }
    if (!decision?.args) {
        return { toolCall: prepared.toolCall, tool: prepared.tool, args: prepared.args };
    }
    try {
        const validatedArgs = validateToolArguments(prepared.tool, {
            ...prepared.toolCall,
            arguments: decision.args,
        });
        return { toolCall: prepared.toolCall, tool: prepared.tool, args: validatedArgs };
    }
    catch (error) {
        return immediateError(prepared.toolCall, error instanceof Error ? error.message : String(error));
    }
}
/** Execute one cleared external tool effect, converting expected tool throws to error output. */
export function executeToolCall(call, gate, onUpdate, toolContext, invocation, context) {
    let acceptingUpdates = true;
    return gate.admit(async () => {
        const admittedContext = withAbortSignal(gate.signal, context);
        admittedContext.abortSignal?.throwIfAborted();
        try {
            const result = await call.tool.execute(call.toolCall.id, call.args, (partial, options) => {
                if (acceptingUpdates)
                    onUpdate(partial, options);
            }, toolContext, invocation, admittedContext);
            return { result, isError: false };
        }
        catch (error) {
            return {
                result: createErrorToolResult(error instanceof Error ? error.message : String(error)),
                isError: true,
            };
        }
        finally {
            acceptingUpdates = false;
        }
    });
}
/** Apply an after-tool patch field by field. */
export function finalizeToolCall(call, executed, patch) {
    const result = patch
        ? {
            ...executed.result,
            content: patch.content === undefined ? executed.result.content : patch.content,
            details: patch.details === undefined ? executed.result.details : patch.details,
            usage: patch.usage === undefined ? executed.result.usage : patch.usage,
            terminate: patch.terminate === undefined ? executed.result.terminate : patch.terminate,
        }
        : executed.result;
    return {
        toolCall: call.toolCall,
        result,
        isError: patch?.isError ?? executed.isError,
        terminate: result.terminate === true,
    };
}
/** Reconstruct the canonical tool result represented by a staged transcript message. */
export function toolResultFromMessage(message, terminate) {
    return {
        content: message.content,
        details: message.details,
        ...(message.usage === undefined ? {} : { usage: message.usage }),
        ...(terminate ? { terminate: true } : {}),
    };
}
/** Convert finalized tool output to the provider-facing transcript message. */
export function createToolResultMessage(call) {
    return {
        role: "toolResult",
        toolCallId: call.toolCall.id,
        toolName: call.toolCall.name,
        content: call.result.content ?? [],
        ...(call.result.details === undefined ? {} : { details: call.result.details }),
        ...(call.result.usage === undefined ? {} : { usage: call.result.usage }),
        isError: call.isError,
        timestamp: Date.now(),
    };
}
//# sourceMappingURL=tools.js.map