import { contentText, } from "../../../pi-ai/sdk-bundle/index.js";
import { convertToLlm, createBranchSummaryMessage, createCompactionSummaryMessage } from "../messages.js";
import { BranchSummaryError, err, ok } from "../types.js";
import { completeSimpleWithRetries, createSummaryRequestOptions, estimateTokens, SUMMARIZATION_SYSTEM_PROMPT, } from "./compaction.js";
import { computeFileLists, createFileOps, extractFileOpsFromMessage, formatFileOperations, serializeConversation, } from "./utils.js";
/** Collect entries that should be summarized before navigating to a different session tree entry. */
export async function collectEntriesForBranchSummary(branch, session, oldTipId, targetId, context) {
    if (!oldTipId) {
        return { entries: [], commonAncestorId: null };
    }
    const oldPath = new Set((await branch.findEntries({ start: oldTipId }, context)).map((entry) => entry.id));
    const targetPath = await branch.findEntries({ start: targetId }, context);
    let commonAncestorId = null;
    for (const entry of targetPath) {
        if (oldPath.has(entry.id)) {
            commonAncestorId = entry.id;
            break;
        }
    }
    const entries = [];
    let current = oldTipId;
    while (current && current !== commonAncestorId) {
        const entry = await session.getEntry(current, context);
        if (!entry)
            throw new Error(`Corrupt session: entry ${current} not found`);
        entries.push(entry);
        current = entry.parentId;
    }
    entries.reverse();
    return { entries, commonAncestorId };
}
function getMessageFromEntry(entry) {
    switch (entry.type) {
        case "message":
            if (entry.message.role === "toolResult")
                return undefined;
            return entry.message;
        case "branch_summary":
            return createBranchSummaryMessage(entry.summary, entry.fromId, entry.timestamp);
        case "compaction":
            return createCompactionSummaryMessage(entry.summary, entry.tokensBefore, entry.timestamp);
        case "custom":
            return undefined;
    }
}
/** Prepare branch entries for summarization within an optional token budget. */
export function prepareBranchEntries(entries, tokenBudget = 0) {
    const messages = [];
    const fileOps = createFileOps();
    let totalTokens = 0;
    for (const entry of entries) {
        if (entry.type !== "branch_summary" ||
            typeof entry.details !== "object" ||
            entry.details === null ||
            Array.isArray(entry.details)) {
            continue;
        }
        if (Array.isArray(entry.details.readFiles)) {
            for (const path of entry.details.readFiles) {
                if (typeof path === "string")
                    fileOps.read.add(path);
            }
        }
        if (Array.isArray(entry.details.modifiedFiles)) {
            for (const path of entry.details.modifiedFiles) {
                if (typeof path === "string")
                    fileOps.edited.add(path);
            }
        }
    }
    for (let i = entries.length - 1; i >= 0; i--) {
        const entry = entries[i];
        const message = getMessageFromEntry(entry);
        if (!message)
            continue;
        extractFileOpsFromMessage(message, fileOps);
        const tokens = estimateTokens(message);
        if (tokenBudget > 0 && totalTokens + tokens > tokenBudget) {
            if (entry.type === "compaction" || entry.type === "branch_summary") {
                if (totalTokens < tokenBudget * 0.9) {
                    messages.unshift(message);
                    totalTokens += tokens;
                }
            }
            break;
        }
        messages.unshift(message);
        totalTokens += tokens;
    }
    return { messages, fileOps, totalTokens };
}
const BRANCH_SUMMARY_PREAMBLE = `The user explored a different conversation branch before returning here.
Summary of that exploration:

`;
const BRANCH_SUMMARY_PROMPT = `Create a structured summary of this conversation branch for context when returning later.

Use this EXACT format:

## Goal
[What was the user trying to accomplish in this branch?]

## Constraints & Preferences
- [Any constraints, preferences, or requirements mentioned]
- [Or "(none)" if none were mentioned]

## Progress
### Done
- [x] [Completed tasks/changes]

### In Progress
- [ ] [Work that was started but not finished]

### Blocked
- [Issues preventing progress, if any]

## Key Decisions
- **[Decision]**: [Brief rationale]

## Next Steps
1. [What should happen next to continue this work]

Keep each section concise. Preserve exact file paths, function names, and error messages.`;
/** Generate a summary for abandoned branch entries. */
export function generateBranchSummary(entries, options, context) {
    const { models, model, customInstructions, replaceInstructions, reserveTokens = 16384, retry, callbacks } = options;
    const contextWindow = model.contextWindow || 128000;
    const preparation = prepareBranchEntries(entries, contextWindow - reserveTokens);
    return generateBranchSummaryWithRequest(preparation, { customInstructions, replaceInstructions }, (aiContext, requestOptions, requestContext) => completeSimpleWithRetries(models, model, aiContext, requestOptions, retry, callbacks, requestContext), context);
}
/** Generate a prepared branch summary through a caller-owned one-request boundary. */
export async function generateBranchSummaryWithRequest(preparation, options, request, context) {
    const { customInstructions, replaceInstructions } = options;
    const { messages, fileOps } = preparation;
    if (messages.length === 0) {
        return ok({ summary: "No content to summarize", readFiles: [], modifiedFiles: [] });
    }
    const llmMessages = convertToLlm(messages);
    const conversationText = serializeConversation(llmMessages);
    let instructions;
    if (replaceInstructions && customInstructions) {
        instructions = customInstructions;
    }
    else if (customInstructions) {
        instructions = `${BRANCH_SUMMARY_PROMPT}\n\nAdditional focus: ${customInstructions}`;
    }
    else {
        instructions = BRANCH_SUMMARY_PROMPT;
    }
    const promptText = `<conversation>\n${conversationText}\n</conversation>\n\n${instructions}`;
    const summarizationMessages = [
        {
            role: "user",
            content: [{ type: "text", text: promptText }],
            timestamp: Date.now(),
        },
    ];
    const response = await request({ systemPrompt: SUMMARIZATION_SYSTEM_PROMPT, messages: summarizationMessages }, createSummaryRequestOptions({ maxTokens: 2048 }, context), context);
    if (response.stopReason === "aborted") {
        return err(new BranchSummaryError("aborted", response.errorMessage || "Branch summary aborted"));
    }
    if (response.stopReason === "error") {
        return err(new BranchSummaryError("summarization_failed", `Branch summary failed: ${response.errorMessage || "Unknown error"}`));
    }
    let summary = contentText(response.content);
    summary = BRANCH_SUMMARY_PREAMBLE + summary;
    const { readFiles, modifiedFiles } = computeFileLists(fileOps);
    summary += formatFileOperations(readFiles, modifiedFiles);
    return ok({
        summary: summary || "No summary generated",
        usage: response.usage,
        readFiles,
        modifiedFiles,
    });
}
//# sourceMappingURL=branch-summarization.js.map