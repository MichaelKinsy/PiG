import { contentText, retryAssistantCall, uuidv7, } from "../../../pi-ai/sdk-bundle/index.js";
import { getTelemetryContext } from "../context.js";
import { convertToLlm, createBranchSummaryMessage, createCompactionSummaryMessage } from "../messages.js";
import { buildContextEntries, sessionEntryToContextMessages } from "../session/context.js";
import { CompactionError, err, ok } from "../types.js";
import { addUsage } from "../utils/usage.js";
import { computeFileLists, createFileOps, extractFileOpsFromMessage, formatFileOperations, serializeConversation, } from "./utils.js";
function safeJsonStringify(value) {
    try {
        return JSON.stringify(value) ?? "undefined";
    }
    catch {
        return "[unserializable]";
    }
}
function extractFileOperations(messages, entries, prevCompactionIndex) {
    const fileOps = createFileOps();
    if (prevCompactionIndex >= 0) {
        const prevCompaction = entries[prevCompactionIndex];
        if (typeof prevCompaction.details === "object" &&
            prevCompaction.details !== null &&
            !Array.isArray(prevCompaction.details)) {
            if (Array.isArray(prevCompaction.details.readFiles)) {
                for (const path of prevCompaction.details.readFiles) {
                    if (typeof path === "string")
                        fileOps.read.add(path);
                }
            }
            if (Array.isArray(prevCompaction.details.modifiedFiles)) {
                for (const path of prevCompaction.details.modifiedFiles) {
                    if (typeof path === "string")
                        fileOps.edited.add(path);
                }
            }
        }
    }
    for (const msg of messages) {
        extractFileOpsFromMessage(msg, fileOps);
    }
    return fileOps;
}
function getMessageFromEntry(entry) {
    if (entry.type === "message") {
        return entry.message;
    }
    if (entry.type === "branch_summary") {
        return createBranchSummaryMessage(entry.summary, entry.fromId, entry.timestamp);
    }
    if (entry.type === "compaction") {
        return createCompactionSummaryMessage(entry.summary, entry.tokensBefore, entry.timestamp);
    }
    return undefined;
}
function getMessageFromEntryForCompaction(entry) {
    if (entry.type === "compaction") {
        return undefined;
    }
    return getMessageFromEntry(entry);
}
export function createSummaryRequestOptions(options, context) {
    return {
        ...options,
        signal: context.abortSignal,
        telemetryContext: getTelemetryContext(context),
        cacheRetention: "none",
        sessionId: options.sessionId ?? uuidv7(),
    };
}
export async function completeSimpleWithRetries(models, model, aiContext, options, retry, callbacks, context) {
    // Summaries are standalone requests, so isolate routing and avoid cache writes that cannot be reused.
    const requestOptions = createSummaryRequestOptions(options, context);
    return retryAssistantCall(() => models.completeSimple(model, aiContext, requestOptions), retry, requestOptions.signal, callbacks);
}
/** Default compaction settings used by the harness. */
export const DEFAULT_COMPACTION_SETTINGS = {
    enabled: true,
    reserveTokens: 16384,
    keepRecentTokens: 20000,
};
/** Calculate total context tokens from provider usage. */
export function calculateContextTokens(usage) {
    return usage.totalTokens || usage.input + usage.output + usage.cacheRead + usage.cacheWrite;
}
function getAssistantUsage(msg) {
    if (msg.role === "assistant" && "usage" in msg) {
        const assistantMsg = msg;
        if (assistantMsg.stopReason !== "aborted" &&
            assistantMsg.stopReason !== "error" &&
            assistantMsg.usage &&
            calculateContextTokens(assistantMsg.usage) > 0) {
            return assistantMsg.usage;
        }
    }
    return undefined;
}
/** Return usage from the last valid assistant message in session entries. */
export function getLastAssistantUsage(entries) {
    for (let i = entries.length - 1; i >= 0; i--) {
        const entry = entries[i];
        if (entry.type === "message") {
            const usage = getAssistantUsage(entry.message);
            if (usage)
                return usage;
        }
    }
    return undefined;
}
function getLastAssistantUsageInfo(messages) {
    for (let i = messages.length - 1; i >= 0; i--) {
        const usage = getAssistantUsage(messages[i]);
        if (usage)
            return { usage, index: i };
    }
    return undefined;
}
/** Estimate context tokens for messages using provider usage when available. */
export function estimateContextTokens(messages) {
    const usageInfo = getLastAssistantUsageInfo(messages);
    if (!usageInfo) {
        let estimated = 0;
        for (const message of messages) {
            estimated += estimateTokens(message);
        }
        return {
            tokens: estimated,
            usageTokens: 0,
            trailingTokens: estimated,
            lastUsageIndex: null,
        };
    }
    const usageTokens = calculateContextTokens(usageInfo.usage);
    let trailingTokens = 0;
    for (let i = usageInfo.index + 1; i < messages.length; i++) {
        trailingTokens += estimateTokens(messages[i]);
    }
    return {
        tokens: usageTokens + trailingTokens,
        usageTokens,
        trailingTokens,
        lastUsageIndex: usageInfo.index,
    };
}
/** Return whether context usage exceeds the configured compaction threshold. */
export function shouldCompact(contextTokens, contextWindow, settings) {
    if (!settings.enabled)
        return false;
    return contextTokens > contextWindow - settings.reserveTokens;
}
const ESTIMATED_IMAGE_CHARS = 4800;
function estimateTextAndImageContentChars(content) {
    if (typeof content === "string") {
        return content.length;
    }
    let chars = 0;
    for (const block of content) {
        if (block.type === "text" && block.text) {
            chars += block.text.length;
        }
        else if (block.type === "image") {
            chars += ESTIMATED_IMAGE_CHARS;
        }
    }
    return chars;
}
/** Estimate token count for one message using a conservative character heuristic. */
export function estimateTokens(message) {
    let chars = 0;
    switch (message.role) {
        case "user": {
            chars = estimateTextAndImageContentChars(message.content);
            return Math.ceil(chars / 4);
        }
        case "assistant": {
            const assistant = message;
            for (const block of assistant.content) {
                if (block.type === "text") {
                    chars += block.text.length;
                }
                else if (block.type === "thinking") {
                    chars += block.thinking.length;
                }
                else if (block.type === "toolCall") {
                    chars += block.name.length + safeJsonStringify(block.arguments).length;
                }
            }
            return Math.ceil(chars / 4);
        }
        case "custom":
        case "toolResult": {
            chars = estimateTextAndImageContentChars(message.content);
            return Math.ceil(chars / 4);
        }
        case "bashExecution": {
            chars = message.command.length + message.output.length;
            return Math.ceil(chars / 4);
        }
        case "branchSummary":
        case "compactionSummary": {
            chars = message.summary.length;
            return Math.ceil(chars / 4);
        }
    }
    return 0;
}
function findValidCutPoints(entries, startIndex, endIndex) {
    const cutPoints = [];
    for (let i = startIndex; i < endIndex; i++) {
        const entry = entries[i];
        switch (entry.type) {
            case "message": {
                const role = entry.message.role;
                switch (role) {
                    case "bashExecution":
                    case "custom":
                    case "branchSummary":
                    case "compactionSummary":
                    case "user":
                    case "assistant":
                        cutPoints.push(i);
                        break;
                    case "toolResult":
                        break;
                }
                break;
            }
            case "compaction":
            case "branch_summary":
            case "custom":
                break;
        }
        if (entry.type === "branch_summary")
            cutPoints.push(i);
    }
    return cutPoints;
}
/** Find the user-visible message that starts the turn containing an entry. */
export function findTurnStartIndex(entries, entryIndex, startIndex) {
    for (let i = entryIndex; i >= startIndex; i--) {
        const entry = entries[i];
        if (entry.type === "branch_summary") {
            return i;
        }
        if (entry.type === "message") {
            const role = entry.message.role;
            if (role === "user" || role === "bashExecution") {
                return i;
            }
        }
    }
    return -1;
}
/** Find the compaction cut point that keeps approximately the requested recent-token budget. */
export function findCutPoint(entries, startIndex, endIndex, keepRecentTokens) {
    const cutPoints = findValidCutPoints(entries, startIndex, endIndex);
    if (cutPoints.length === 0) {
        return { firstKeptEntryIndex: startIndex, turnStartIndex: -1, isSplitTurn: false };
    }
    let accumulatedTokens = 0;
    let cutIndex = cutPoints[0];
    for (let i = endIndex - 1; i >= startIndex; i--) {
        const entry = entries[i];
        if (entry.type !== "message")
            continue;
        const messageTokens = estimateTokens(entry.message);
        accumulatedTokens += messageTokens;
        if (accumulatedTokens >= keepRecentTokens) {
            for (let c = 0; c < cutPoints.length; c++) {
                if (cutPoints[c] >= i) {
                    cutIndex = cutPoints[c];
                    break;
                }
            }
            break;
        }
    }
    while (cutIndex > startIndex) {
        const prevEntry = entries[cutIndex - 1];
        if (prevEntry.type === "compaction") {
            break;
        }
        if (prevEntry.type === "message") {
            break;
        }
        cutIndex--;
    }
    const cutEntry = entries[cutIndex];
    const isUserMessage = cutEntry.type === "message" && cutEntry.message.role === "user";
    const turnStartIndex = isUserMessage ? -1 : findTurnStartIndex(entries, cutIndex, startIndex);
    return {
        firstKeptEntryIndex: cutIndex,
        turnStartIndex,
        isSplitTurn: !isUserMessage && turnStartIndex !== -1,
    };
}
export const SUMMARIZATION_SYSTEM_PROMPT = `You are a context summarization assistant. Your task is to read a conversation between a user and an AI assistant, then produce a structured summary following the exact format specified.

Do NOT continue the conversation. Do NOT respond to any questions in the conversation. ONLY output the structured summary.`;
const SUMMARIZATION_PROMPT = `The messages above are a conversation to summarize. Create a structured context checkpoint summary that another LLM will use to continue the work.

Use this EXACT format:

## Goal
[What is the user trying to accomplish? Can be multiple items if the session covers different tasks.]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned by user]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Current work]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [Ordered list of what should happen next]

## Critical Context
- [Any data, examples, or references needed to continue]
- [Or "(none)" if not applicable]

Keep each section concise. Preserve exact file paths, function names, and error messages.`;
const UPDATE_SUMMARIZATION_PROMPT = `The messages above are NEW conversation messages to incorporate into the existing summary provided in <previous-summary> tags.

Update the existing structured summary with new information. RULES:
- PRESERVE all existing information from the previous summary
- ADD new progress, decisions, and context from the new messages
- UPDATE the Progress section: move items from "In Progress" to "Done" when completed
- UPDATE "Next Steps" based on what was accomplished
- PRESERVE exact file paths, function names, and error messages
- If something is no longer relevant, you may remove it

Use this EXACT format:

## Goal
[Preserve existing goals, add new ones if the task expanded]

## Constraints & Preferences
- [Preserve existing, add new ones discovered]

## Progress
### Done
- [x] [Include previously done items AND newly completed items]

### In Progress
- [ ] [Current work - update based on progress]

### Blocked
- [Current blockers - remove if resolved]

## Key Decisions
- **[Decision]**: [Brief rationale] (preserve all previous, add new)

## Next Steps
1. [Update based on current state]

## Critical Context
- [Preserve important context, add new if needed]

Keep each section concise. Preserve exact file paths, function names, and error messages.`;
/** Generate or update a conversation summary for compaction. */
export async function generateSummary(currentMessages, models, model, reserveTokens, customInstructions, previousSummary, thinkingLevel, retry, callbacks, context) {
    const result = await generateSummaryWithUsage(currentMessages, models, model, reserveTokens, customInstructions, previousSummary, thinkingLevel, retry, callbacks, context);
    return result.ok ? ok(result.value.text) : err(result.error);
}
/** Generate or update a conversation summary and return its provider usage. */
export function generateSummaryWithUsage(currentMessages, models, model, reserveTokens, customInstructions, previousSummary, thinkingLevel, retry, callbacks, context) {
    return generateSummaryWithRequest(currentMessages, { model, reserveTokens, customInstructions, previousSummary, thinkingLevel }, (aiContext, options, requestContext) => completeSimpleWithRetries(models, model, aiContext, options, retry, callbacks, requestContext), context);
}
/** Generate one summary through a caller-owned one-request boundary. */
export async function generateSummaryWithRequest(currentMessages, options, request, context) {
    const { model, reserveTokens, customInstructions, previousSummary, thinkingLevel } = options;
    const maxTokens = Math.min(Math.floor(0.8 * reserveTokens), model.maxTokens > 0 ? model.maxTokens : Number.POSITIVE_INFINITY);
    let basePrompt = previousSummary ? UPDATE_SUMMARIZATION_PROMPT : SUMMARIZATION_PROMPT;
    if (customInstructions) {
        basePrompt = `${basePrompt}\n\nAdditional focus: ${customInstructions}`;
    }
    const llmMessages = convertToLlm(currentMessages);
    const conversationText = serializeConversation(llmMessages);
    let promptText = `<conversation>\n${conversationText}\n</conversation>\n\n`;
    if (previousSummary) {
        promptText += `<previous-summary>\n${previousSummary}\n</previous-summary>\n\n`;
    }
    promptText += basePrompt;
    const summarizationMessages = [
        {
            role: "user",
            content: [{ type: "text", text: promptText }],
            timestamp: Date.now(),
        },
    ];
    const completionOptions = model.reasoning && thinkingLevel && thinkingLevel !== "off"
        ? { maxTokens, reasoning: thinkingLevel }
        : { maxTokens };
    const response = await request({ systemPrompt: SUMMARIZATION_SYSTEM_PROMPT, messages: summarizationMessages }, createSummaryRequestOptions(completionOptions, context), context);
    if (response.stopReason === "aborted") {
        return err(new CompactionError("aborted", response.errorMessage || "Summarization aborted"));
    }
    if (response.stopReason === "error") {
        return err(new CompactionError("summarization_failed", `Summarization failed: ${response.errorMessage || "Unknown error"}`));
    }
    const textContent = contentText(response.content);
    return ok({ text: textContent, usage: response.usage });
}
/** Prepare session entries for compaction, or return undefined when compaction is not applicable. */
export function prepareCompaction(pathEntries, settings) {
    if (pathEntries.length === 0 || pathEntries[pathEntries.length - 1].type === "compaction") {
        return ok(undefined);
    }
    let prevCompactionIndex = -1;
    for (let i = pathEntries.length - 1; i >= 0; i--) {
        if (pathEntries[i].type === "compaction") {
            prevCompactionIndex = i;
            break;
        }
    }
    let previousSummary;
    let compactableEntries = pathEntries;
    if (prevCompactionIndex >= 0) {
        const prevCompaction = pathEntries[prevCompactionIndex];
        previousSummary = prevCompaction.summary;
        const virtualRetainedEntries = prevCompaction.retainedTail.map((message, index) => ({
            type: "message",
            id: `${prevCompaction.id}:retained:${index}`,
            parentId: index === 0 ? prevCompaction.id : `${prevCompaction.id}:retained:${index - 1}`,
            seq: prevCompaction.seq,
            timestamp: message.timestamp,
            message,
        }));
        compactableEntries = [...virtualRetainedEntries, ...pathEntries.slice(prevCompactionIndex + 1)];
    }
    const boundaryEnd = compactableEntries.length;
    const tokensBefore = estimateContextTokens(buildContextEntries(pathEntries).flatMap(sessionEntryToContextMessages)).tokens;
    const cutPoint = findCutPoint(compactableEntries, 0, boundaryEnd, settings.keepRecentTokens);
    const historyEnd = cutPoint.isSplitTurn ? cutPoint.turnStartIndex : cutPoint.firstKeptEntryIndex;
    const messagesToSummarize = [];
    for (let i = 0; i < historyEnd; i++) {
        const msg = getMessageFromEntryForCompaction(compactableEntries[i]);
        if (msg)
            messagesToSummarize.push(msg);
    }
    const turnPrefixMessages = [];
    if (cutPoint.isSplitTurn) {
        for (let i = cutPoint.turnStartIndex; i < cutPoint.firstKeptEntryIndex; i++) {
            const msg = getMessageFromEntryForCompaction(compactableEntries[i]);
            if (msg)
                turnPrefixMessages.push(msg);
        }
    }
    const retainedTail = [];
    for (let i = cutPoint.firstKeptEntryIndex; i < boundaryEnd; i++) {
        const msg = getMessageFromEntryForCompaction(compactableEntries[i]);
        if (msg)
            retainedTail.push(msg);
    }
    const fileOps = extractFileOperations(messagesToSummarize, pathEntries, prevCompactionIndex);
    if (cutPoint.isSplitTurn) {
        for (const msg of turnPrefixMessages) {
            extractFileOpsFromMessage(msg, fileOps);
        }
    }
    return ok({
        messagesToSummarize,
        turnPrefixMessages,
        retainedTail,
        isSplitTurn: cutPoint.isSplitTurn,
        tokensBefore,
        previousSummary,
        fileOps,
        settings,
    });
}
const TURN_PREFIX_SUMMARIZATION_PROMPT = `This is the PREFIX of a turn that was too large to keep. The SUFFIX (recent work) is retained.

Summarize the prefix to provide context for the retained suffix:

## Original Request
[What did the user ask for in this turn?]

## Early Progress
- [Key decisions and work done in the prefix]

## Context for Suffix
- [Information needed to understand the retained recent work]

Be concise. Focus on what's needed to understand the kept suffix.`;
export { serializeConversation } from "./utils.js";
/** Generate compaction summary data from prepared session history. */
export function compact(preparation, models, model, customInstructions, thinkingLevel, retry, callbacks, context) {
    return compactWithRequest(preparation, { model, customInstructions, thinkingLevel }, (aiContext, options, requestContext) => completeSimpleWithRetries(models, model, aiContext, options, retry, callbacks, requestContext), context);
}
/** Generate compaction data through a caller-owned boundary for each provider request. */
export async function compactWithRequest(preparation, options, request, context) {
    const { model, customInstructions, thinkingLevel } = options;
    const { messagesToSummarize, turnPrefixMessages, retainedTail, isSplitTurn, tokensBefore, previousSummary, fileOps, settings, } = preparation;
    let summary;
    let summaryUsage;
    if (isSplitTurn && turnPrefixMessages.length > 0) {
        let historyText = "No prior history.";
        let historyUsage;
        if (messagesToSummarize.length > 0) {
            const historyResult = await generateSummaryWithRequest(messagesToSummarize, { model, reserveTokens: settings.reserveTokens, customInstructions, previousSummary, thinkingLevel }, request, context);
            if (!historyResult.ok)
                return err(historyResult.error);
            historyText = historyResult.value.text;
            historyUsage = historyResult.value.usage;
        }
        const turnPrefixResult = await generateTurnPrefixSummary(turnPrefixMessages, model, settings.reserveTokens, thinkingLevel, request, context);
        if (!turnPrefixResult.ok)
            return err(turnPrefixResult.error);
        summary = `${historyText}\n\n---\n\n**Turn Context (split turn):**\n\n${turnPrefixResult.value.text}`;
        summaryUsage = historyUsage ? addUsage(historyUsage, turnPrefixResult.value.usage) : turnPrefixResult.value.usage;
    }
    else {
        const summaryResult = await generateSummaryWithRequest(messagesToSummarize, { model, reserveTokens: settings.reserveTokens, customInstructions, previousSummary, thinkingLevel }, request, context);
        if (!summaryResult.ok)
            return err(summaryResult.error);
        summary = summaryResult.value.text;
        summaryUsage = summaryResult.value.usage;
    }
    const { readFiles, modifiedFiles } = computeFileLists(fileOps);
    summary += formatFileOperations(readFiles, modifiedFiles);
    const details = { readFiles, modifiedFiles };
    return ok({ summary, tokensBefore, usage: summaryUsage, retainedTail, details });
}
async function generateTurnPrefixSummary(messages, model, reserveTokens, thinkingLevel, request, context) {
    const maxTokens = Math.min(Math.floor(0.5 * reserveTokens), model.maxTokens > 0 ? model.maxTokens : Number.POSITIVE_INFINITY);
    const llmMessages = convertToLlm(messages);
    const conversationText = serializeConversation(llmMessages);
    const promptText = `<conversation>\n${conversationText}\n</conversation>\n\n${TURN_PREFIX_SUMMARIZATION_PROMPT}`;
    const summarizationMessages = [
        {
            role: "user",
            content: [{ type: "text", text: promptText }],
            timestamp: Date.now(),
        },
    ];
    const completionOptions = model.reasoning && thinkingLevel && thinkingLevel !== "off"
        ? { maxTokens, reasoning: thinkingLevel }
        : { maxTokens };
    const response = await request({ systemPrompt: SUMMARIZATION_SYSTEM_PROMPT, messages: summarizationMessages }, createSummaryRequestOptions(completionOptions, context), context);
    if (response.stopReason === "aborted") {
        return err(new CompactionError("aborted", response.errorMessage || "Turn prefix summarization aborted"));
    }
    if (response.stopReason === "error") {
        return err(new CompactionError("summarization_failed", `Turn prefix summarization failed: ${response.errorMessage || "Unknown error"}`));
    }
    return ok({
        text: contentText(response.content),
        usage: response.usage,
    });
}
//# sourceMappingURL=compaction.js.map