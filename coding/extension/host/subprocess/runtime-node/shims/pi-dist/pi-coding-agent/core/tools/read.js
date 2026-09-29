import { constants } from "fs";
import { access as fsAccess, readFile as fsReadFile } from "fs/promises";
import { Type } from "../../../../typebox.mjs";
import { processImage } from "../../utils/image-process.js";
import { detectSupportedImageMimeTypeFromFile } from "../../utils/mime.js";
import { resolveReadPathAsync } from "./path-utils.js";
import { readRenderers } from "./renderers/read.js";
import { wrapToolDefinition } from "./tool-definition-wrapper.js";
import { DEFAULT_MAX_BYTES, DEFAULT_MAX_LINES, formatSize, truncateHead } from "./truncate.js";
const readSchema = Type.Object({
    path: Type.String({ description: "Path to the file to read (relative or absolute)" }),
    offset: Type.Optional(Type.Number({ description: "Line number to start reading from (1-indexed)" })),
    limit: Type.Optional(Type.Number({ description: "Maximum number of lines to read" })),
});
export const readToolSystemPromptContribution = {
    snippet: "Read file contents",
    guidelines: ["Use read to examine files instead of cat or sed."],
};
const defaultReadOperations = {
    readFile: (path) => fsReadFile(path),
    access: (path) => fsAccess(path, constants.R_OK),
    detectImageMimeType: detectSupportedImageMimeTypeFromFile,
};
function getNonVisionImageNote(model) {
    if (!model || model.input.includes("image")) {
        return undefined;
    }
    return "[Current model does not support images. The image will be omitted from this request.]";
}
export function createReadToolDefinition(cwd, options) {
    const autoResizeImages = options?.autoResizeImages ?? true;
    const fallbackResizeOptions = options?.resizeOptions;
    const ops = options?.operations ?? defaultReadOperations;
    return {
        name: "read",
        label: "read",
        description: `Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to ${DEFAULT_MAX_LINES} lines or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.`,
        promptSnippet: readToolSystemPromptContribution.snippet,
        promptGuidelines: [...readToolSystemPromptContribution.guidelines],
        parameters: readSchema,
        constrainedSampling: { type: "json_schema", strict: "prefer" },
        async execute(_toolCallId, { path, offset, limit }, signal, _onUpdate, ctx) {
            return new Promise((resolve, reject) => {
                if (signal?.aborted) {
                    reject(new Error("Operation aborted"));
                    return;
                }
                let aborted = false;
                const onAbort = () => {
                    aborted = true;
                    reject(new Error("Operation aborted"));
                };
                signal?.addEventListener("abort", onAbort, { once: true });
                (async () => {
                    try {
                        const absolutePath = await resolveReadPathAsync(path, ctx?.cwd || cwd);
                        if (aborted)
                            return;
                        // Check if file exists and is readable.
                        await ops.access(absolutePath);
                        if (aborted)
                            return;
                        const mimeType = ops.detectImageMimeType ? await ops.detectImageMimeType(absolutePath) : undefined;
                        let content;
                        let details;
                        const nonVisionImageNote = getNonVisionImageNote(ctx?.model);
                        if (mimeType) {
                            // Read image as binary.
                            const buffer = await ops.readFile(absolutePath);
                            const processed = await processImage(buffer, mimeType, {
                                autoResizeImages,
                                resizeOptions: ctx?.model?.inputLimits?.images?.resize ?? fallbackResizeOptions,
                            });
                            if (!processed.ok) {
                                let textNote = `Read image file [${mimeType}]\n${processed.message}`;
                                if (nonVisionImageNote)
                                    textNote += `\n${nonVisionImageNote}`;
                                content = [{ type: "text", text: textNote }];
                            }
                            else {
                                let textNote = `Read image file [${processed.mimeType}]`;
                                if (processed.hints.length > 0)
                                    textNote += `\n${processed.hints.join("\n")}`;
                                if (nonVisionImageNote)
                                    textNote += `\n${nonVisionImageNote}`;
                                content = [
                                    { type: "text", text: textNote },
                                    { type: "image", data: processed.data, mimeType: processed.mimeType },
                                ];
                            }
                        }
                        else {
                            // Read text content.
                            const buffer = await ops.readFile(absolutePath);
                            const textContent = buffer.toString("utf-8");
                            const allLines = textContent.split("\n");
                            const totalFileLines = allLines.length;
                            // Apply offset if specified. Convert from 1-indexed input to 0-indexed array access.
                            const startLine = offset ? Math.max(0, offset - 1) : 0;
                            const startLineDisplay = startLine + 1;
                            // Check if offset is out of bounds.
                            if (startLine >= allLines.length) {
                                throw new Error(`Offset ${offset} is beyond end of file (${allLines.length} lines total)`);
                            }
                            let selectedContent;
                            let userLimitedLines;
                            // If limit is specified by the user, honor it first. Otherwise truncateHead decides.
                            if (limit !== undefined) {
                                const endLine = Math.min(startLine + limit, allLines.length);
                                selectedContent = allLines.slice(startLine, endLine).join("\n");
                                userLimitedLines = endLine - startLine;
                            }
                            else {
                                selectedContent = allLines.slice(startLine).join("\n");
                            }
                            // Apply truncation, respecting both line and byte limits.
                            const truncation = truncateHead(selectedContent);
                            let outputText;
                            if (truncation.firstLineExceedsLimit) {
                                // First line alone exceeds the byte limit. Point the model at a bash fallback.
                                const firstLineSize = formatSize(Buffer.byteLength(allLines[startLine], "utf-8"));
                                outputText = `[Line ${startLineDisplay} is ${firstLineSize}, exceeds ${formatSize(DEFAULT_MAX_BYTES)} limit. Use bash: sed -n '${startLineDisplay}p' ${path} | head -c ${DEFAULT_MAX_BYTES}]`;
                                details = { truncation };
                            }
                            else if (truncation.truncated) {
                                // Truncation occurred. Build an actionable continuation notice.
                                const endLineDisplay = startLineDisplay + truncation.outputLines - 1;
                                const nextOffset = endLineDisplay + 1;
                                outputText = truncation.content;
                                if (truncation.truncatedBy === "lines") {
                                    outputText += `\n\n[Showing lines ${startLineDisplay}-${endLineDisplay} of ${totalFileLines}. Use offset=${nextOffset} to continue.]`;
                                }
                                else {
                                    outputText += `\n\n[Showing lines ${startLineDisplay}-${endLineDisplay} of ${totalFileLines} (${formatSize(DEFAULT_MAX_BYTES)} limit). Use offset=${nextOffset} to continue.]`;
                                }
                                details = { truncation };
                            }
                            else if (userLimitedLines !== undefined && startLine + userLimitedLines < allLines.length) {
                                // User-specified limit stopped early, but the file still has more content.
                                const remaining = allLines.length - (startLine + userLimitedLines);
                                const nextOffset = startLine + userLimitedLines + 1;
                                outputText = `${truncation.content}\n\n[${remaining} more lines in file. Use offset=${nextOffset} to continue.]`;
                            }
                            else {
                                // No truncation and no remaining user-limited content.
                                outputText = truncation.content;
                            }
                            content = [{ type: "text", text: outputText }];
                        }
                        if (aborted)
                            return;
                        signal?.removeEventListener("abort", onAbort);
                        resolve({ content, details });
                    }
                    catch (error) {
                        signal?.removeEventListener("abort", onAbort);
                        if (!aborted)
                            reject(error);
                    }
                })();
            });
        },
        ...readRenderers,
    };
}
export function createReadTool(cwd, options) {
    return wrapToolDefinition(createReadToolDefinition(cwd, options));
}
//# sourceMappingURL=read.js.map