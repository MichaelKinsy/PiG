import { constants } from "fs";
import { access as fsAccess, readFile as fsReadFile, writeFile as fsWriteFile } from "fs/promises";
import { Type } from "../../../../typebox.mjs";
import { splitBom } from "../../utils/text.js";
import { applyEditsToNormalizedContent, detectLineEnding, generateDiffString, generateUnifiedPatch, normalizeToLF, restoreLineEndings, } from "./edit-diff.js";
import { withFileMutationQueue } from "./file-mutation-queue.js";
import { resolveToCwd } from "./path-utils.js";
import { editRenderers } from "./renderers/edit.js";
import { wrapToolDefinition } from "./tool-definition-wrapper.js";
const replaceEditSchema = Type.Object({
    oldText: Type.String({
        description: "Exact text for one targeted replacement. It must be unique in the original file and must not overlap with any other edits[].oldText in the same call.",
    }),
    newText: Type.String({ description: "Replacement text for this targeted edit." }),
}, {});
const editSchema = Type.Object({
    path: Type.String({ description: "Path to the file to edit (relative or absolute)" }),
    edits: Type.Array(replaceEditSchema, {
        description: "One or more targeted replacements. Each edit is matched against the original file, not incrementally. Do not include overlapping or nested edits. If two changes touch the same block or nearby lines, merge them into one edit instead.",
    }),
}, {});
export const editToolSystemPromptContribution = {
    snippet: "Make precise file edits with exact text replacement, including multiple disjoint edits in one call",
    guidelines: [
        "Use edit for precise changes (edits[].oldText must match exactly)",
        "When changing multiple separate locations in one file, use one edit call with multiple entries in edits[] instead of multiple edit calls",
        "Each edits[].oldText is matched against the original file, not after earlier edits are applied. Do not emit overlapping or nested edits. Merge nearby changes into one edit.",
        "Keep edits[].oldText as small as possible while still being unique in the file. Do not pad with large unchanged regions.",
    ],
};
function isSingleEditInput(value) {
    if (!value || typeof value !== "object" || Array.isArray(value)) {
        return false;
    }
    const edit = value;
    return typeof edit.oldText === "string" && typeof edit.newText === "string";
}
const defaultEditOperations = {
    readFile: (path) => fsReadFile(path),
    writeFile: (path, content) => fsWriteFile(path, content, "utf-8"),
    access: (path) => fsAccess(path, constants.R_OK | constants.W_OK),
};
function prepareEditArguments(input) {
    if (!input || typeof input !== "object") {
        return input;
    }
    const args = input;
    // Some models (Opus 4.6, GLM-5.1) send edits as a JSON string instead of an array.
    // Others send a single edit object instead of a one-element edits array.
    if (typeof args.edits === "string") {
        try {
            const parsed = JSON.parse(args.edits);
            if (Array.isArray(parsed)) {
                args.edits = parsed;
            }
            else if (isSingleEditInput(parsed)) {
                args.edits = [parsed];
            }
        }
        catch { }
    }
    else if (isSingleEditInput(args.edits)) {
        args.edits = [args.edits];
    }
    const legacy = args;
    if (typeof legacy.oldText !== "string" || typeof legacy.newText !== "string") {
        return args;
    }
    const edits = Array.isArray(legacy.edits) ? [...legacy.edits] : [];
    edits.push({ oldText: legacy.oldText, newText: legacy.newText });
    const { oldText: _oldText, newText: _newText, ...rest } = legacy;
    return { ...rest, edits };
}
function validateEditInput(input) {
    if (!Array.isArray(input.edits) || input.edits.length === 0) {
        throw new Error("Edit tool input is invalid. edits must contain at least one replacement.");
    }
    return { path: input.path, edits: input.edits };
}
export function createEditToolDefinition(cwd, options) {
    const ops = options?.operations ?? defaultEditOperations;
    return {
        name: "edit",
        label: "edit",
        description: "Edit a single file using exact text replacement. Every edits[].oldText must match a unique, non-overlapping region of the original file. If two changes affect the same block or nearby lines, merge them into one edit instead of emitting overlapping edits. Do not include large unchanged regions just to connect distant changes.",
        promptSnippet: editToolSystemPromptContribution.snippet,
        promptGuidelines: [...editToolSystemPromptContribution.guidelines],
        parameters: editSchema,
        constrainedSampling: { type: "json_schema", strict: "prefer" },
        renderShell: "self",
        prepareArguments: prepareEditArguments,
        async execute(_toolCallId, input, signal, _onUpdate, ctx) {
            const { path, edits } = validateEditInput(input);
            const absolutePath = resolveToCwd(path, ctx?.cwd || cwd);
            return withFileMutationQueue(absolutePath, async () => {
                // Do not reject from an abort event listener here: that would release the
                // mutation queue while an in-flight filesystem operation may still finish.
                // Checking signal.aborted after each await observes the same aborts while
                // keeping the queue locked until the current operation has settled.
                const throwIfAborted = () => {
                    if (signal?.aborted)
                        throw new Error("Operation aborted");
                };
                throwIfAborted();
                // Check if file exists.
                try {
                    await ops.access(absolutePath);
                }
                catch (error) {
                    throwIfAborted();
                    const errorMessage = error instanceof Error && "code" in error ? `Error code: ${error.code}` : String(error);
                    throw new Error(`Could not edit file: ${path}. ${errorMessage}.`);
                }
                throwIfAborted();
                // Read the file.
                const buffer = await ops.readFile(absolutePath);
                const rawContent = buffer.toString("utf-8");
                throwIfAborted();
                // Strip BOM before matching. The model will not include an invisible BOM in oldText.
                const { bom, text: content } = splitBom(rawContent);
                const originalEnding = detectLineEnding(content);
                const normalizedContent = normalizeToLF(content);
                const { baseContent, newContent } = applyEditsToNormalizedContent(normalizedContent, edits, path);
                throwIfAborted();
                const finalContent = bom + restoreLineEndings(newContent, originalEnding);
                await ops.writeFile(absolutePath, finalContent);
                throwIfAborted();
                const diffResult = generateDiffString(baseContent, newContent);
                const patch = generateUnifiedPatch(path, baseContent, newContent);
                return {
                    content: [
                        {
                            type: "text",
                            text: `Successfully replaced ${edits.length} block(s) in ${path}.`,
                        },
                    ],
                    details: { diff: diffResult.diff, patch, firstChangedLine: diffResult.firstChangedLine },
                };
            });
        },
        ...editRenderers,
    };
}
export function createEditTool(cwd, options) {
    return wrapToolDefinition(createEditToolDefinition(cwd, options));
}
//# sourceMappingURL=edit.js.map