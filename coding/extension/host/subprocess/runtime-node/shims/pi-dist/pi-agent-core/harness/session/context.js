import { createBranchSummaryMessage, createCompactionSummaryMessage } from "../messages.js";
export function buildContextEntries(pathEntries) {
    let compaction;
    let compactionIndex = -1;
    for (let index = pathEntries.length - 1; index >= 0; index--) {
        const entry = pathEntries[index];
        if (entry?.type === "compaction") {
            compaction = entry;
            compactionIndex = index;
            break;
        }
    }
    return compaction === undefined ? [...pathEntries] : [compaction, ...pathEntries.slice(compactionIndex + 1)];
}
function isContextMessage(message) {
    return (message.role !== "assistant" ||
        (message.stopReason !== "error" && message.stopReason !== "aborted" && message.stopReason !== "deferred"));
}
export function sessionEntryToContextMessages(entry) {
    switch (entry.type) {
        case "message":
            return isContextMessage(entry.message) ? [entry.message] : [];
        case "compaction":
            return [
                createCompactionSummaryMessage(entry.summary, entry.tokensBefore, entry.timestamp),
                ...entry.retainedTail.filter(isContextMessage),
            ];
        case "branch_summary":
            return entry.summary ? [createBranchSummaryMessage(entry.summary, entry.fromId, entry.timestamp)] : [];
        case "custom":
            return [];
    }
}
export async function buildSessionContext(pathEntries, options, context) {
    options ??= {};
    const entries = buildContextEntries(pathEntries);
    const messages = [];
    for (const entry of entries) {
        if (entry.type !== "custom") {
            messages.push(...sessionEntryToContextMessages(entry));
            continue;
        }
        const projector = options.entryProjectors?.[entry.customType];
        if (projector !== undefined)
            messages.push(...((await projector(entry, context)) ?? []));
    }
    return messages;
}
//# sourceMappingURL=context.js.map