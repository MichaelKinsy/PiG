function toJsonAssistantMessageEvent(event) {
    if (event.type === "toolcall_start") {
        const toolCall = event.partial.content[event.contentIndex];
        if (toolCall?.type !== "toolCall") {
            throw new Error(`toolcall_start content at index ${event.contentIndex} is not a tool call`);
        }
        const { partial: _partial, ...deltaEvent } = event;
        return { ...deltaEvent, id: toolCall.id, toolName: toolCall.name };
    }
    if (!("partial" in event)) {
        return event;
    }
    const { partial: _partial, ...deltaEvent } = event;
    return deltaEvent;
}
export function toJsonEvent(event) {
    if (event.type !== "message_update") {
        return event;
    }
    if (event.message.role !== "assistant") {
        throw new Error("message_update message is not an assistant message");
    }
    return {
        type: "message_update",
        usage: event.message.usage,
        assistantMessageEvent: toJsonAssistantMessageEvent(event.assistantMessageEvent),
    };
}
//# sourceMappingURL=json-event.js.map