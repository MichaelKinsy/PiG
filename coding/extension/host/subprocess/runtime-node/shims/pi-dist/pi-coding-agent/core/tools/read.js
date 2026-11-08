import { constants } from "fs";
import { access as fsAccess, open as fsOpen, readFile as fsReadFile } from "fs/promises";
import { Type } from "../../../../typebox.mjs";
import { TextDecoder } from "util";
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
/**
 * Result for programmatic callers such as codemode scripts: the text for text files, and an image
 * block for images that codemode's `image()` accepts. `note` is the text that goes with the image,
 * such as resize hints. Property descriptions are left out so the type stays on one line in tool
 * descriptions.
 */
const readOutputSchema = Type.Union([
    Type.String(),
    Type.Object({ type: Type.Literal("image"), data: Type.String(), mimeType: Type.String(), note: Type.String() }),
]);
const defaultReadOperations = {
    readFile: (path) => fsReadFile(path),
    createReadStream: readLocalFileChunks,
    access: (path) => fsAccess(path, constants.R_OK),
    detectImageMimeType: detectSupportedImageMimeTypeFromFile,
};
/**
 * Stream a local file the way fs.readFile reads it: a regular file up to its size when opened, so text
 * appended during the read is not counted and a growing file still ends; a file reporting size 0 (such
 * as /proc files) and other file types until EOF.
 */
async function* readLocalFileChunks(path, { signal }) {
    const handle = await fsOpen(path, "r");
    try {
        const stats = await handle.stat();
        const end = stats.isFile() && stats.size > 0 ? stats.size - 1 : undefined;
        yield* handle.createReadStream({ end, signal, autoClose: false });
    }
    finally {
        await handle.close();
    }
}
/** The image block and its note, or the text for text files and images that could not be processed. */
function toReadOutput(content) {
    const text = content.find((block) => block.type === "text")?.text ?? "";
    const image = content.find((block) => block.type === "image");
    return image ? { type: "image", data: image.data, mimeType: image.mimeType, note: text } : text;
}
function getNonVisionImageNote(model) {
    if (!model || model.input.includes("image")) {
        return undefined;
    }
    return "[Current model does not support images. The image will be omitted from this request.]";
}
/**
 * Whether a range can be streamed. Other values (fractional, negative limit) keep the whole-file path,
 * whose array slicing defines what they select, for example limit -1 as all lines but the last.
 */
function isStreamableRange(offset, limit) {
    return ((offset === undefined || Number.isInteger(offset)) &&
        (limit === undefined || (Number.isInteger(limit) && limit >= 0)));
}
/** Select lines from a whole file in memory: for operations without createReadStream and unusual ranges. */
function selectLinesFromBuffer(buffer, startLine, limit) {
    const allLines = buffer.toString("utf-8").split("\n");
    const endLine = limit === undefined ? undefined : Math.min(startLine + limit, allLines.length);
    const truncation = truncateHead(allLines.slice(startLine, endLine).join("\n"));
    const firstLineBytes = truncation.firstLineExceedsLimit ? Buffer.byteLength(allLines[startLine], "utf-8") : 0;
    return { totalFileLines: allLines.length, truncation, firstLineBytes };
}
/**
 * Select lines from a file read in chunks. Only lines truncateHead can show are stored; the rest are
 * counted, so memory does not grow with the file. Stops reading when the signal aborts.
 */
async function selectLinesFromStream(chunks, startLine, limit, signal) {
    const endLine = limit === undefined ? Number.POSITIVE_INFINITY : startLine + limit;
    // Non-fatal UTF-8 that keeps a byte order mark decodes like Buffer.toString("utf-8").
    const decoder = new TextDecoder("utf-8", { ignoreBOM: true });
    const storedLines = [];
    let storedBytes = 0;
    let storedPastCaps = false;
    let line = 0;
    let selectedLines = 0;
    let selectedBytes = 0;
    let firstLineBytes = 0;
    let lastLineEmpty = true;
    // The selected line being read: its text, while it is stored, and its size.
    let pieces = [];
    let lineBytes = 0;
    let lineHasText = false;
    const appendToLine = (text, start, end) => {
        lineHasText = true;
        if (!pieces)
            return;
        const piece = text.slice(start, end);
        // A line over the byte cap is never shown, and a first line over it is only measured: never store
        // all of a 1 GB line.
        if (lineBytes <= DEFAULT_MAX_BYTES)
            pieces.push(piece);
        lineBytes += Buffer.byteLength(piece, "utf-8");
    };
    const finishLine = () => {
        if (pieces) {
            if (selectedLines === 0)
                firstLineBytes = lineBytes;
            // Storing continues one line past the caps: truncateHead ignores a trailing empty line, so the
            // line after the cap must be there for it to decide truncation as for the full text.
            if (storedLines.length > DEFAULT_MAX_LINES || storedBytes > DEFAULT_MAX_BYTES)
                storedPastCaps = true;
            const text = pieces.join("");
            storedBytes += Buffer.byteLength(text, "utf-8") + (storedLines.length > 0 ? 1 : 0);
            storedLines.push(text);
        }
        selectedLines++;
        lastLineEmpty = !lineHasText;
        pieces = storedPastCaps ? undefined : [];
        lineBytes = 0;
        lineHasText = false;
    };
    const select = (text) => {
        let position = 0;
        // Start of the selected text in this piece, measured as a whole: the joined lines' size.
        let selectionStart = -1;
        while (true) {
            const selected = line >= startLine && line < endLine;
            if (selected && selectionStart === -1)
                selectionStart = position;
            const newline = text.indexOf("\n", position);
            const end = newline === -1 ? text.length : newline;
            if (selected && end > position)
                appendToLine(text, position, end);
            if (newline === -1)
                break;
            if (selected) {
                finishLine();
                if (line + 1 === endLine) {
                    selectedBytes += Buffer.byteLength(text.slice(selectionStart, newline), "utf-8");
                    selectionStart = -1;
                }
            }
            line++;
            position = newline + 1;
        }
        if (selectionStart !== -1)
            selectedBytes += Buffer.byteLength(text.slice(selectionStart), "utf-8");
    };
    for await (const chunk of chunks) {
        // Leaving the loop closes the stream.
        if (signal?.aborted)
            throw new Error("Operation aborted");
        select(decoder.decode(chunk, { stream: true }));
    }
    select(decoder.decode());
    if (line >= startLine && line < endLine)
        finishLine();
    const truncation = {
        ...truncateHead(storedLines.join("\n")),
        // truncateHead's counts for the joined selected lines, which drop a trailing empty line.
        totalLines: selectedBytes === 0 ? 0 : selectedLines - (lastLineEmpty ? 1 : 0),
        totalBytes: selectedBytes,
    };
    return { totalFileLines: line + 1, truncation, firstLineBytes };
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
        outputSchema: readOutputSchema,
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
                            // Read text content. Convert offset from 1-indexed input to 0-indexed line access.
                            const startLine = offset ? Math.max(0, offset - 1) : 0;
                            const startLineDisplay = startLine + 1;
                            const { totalFileLines, truncation, firstLineBytes } = ops.createReadStream && isStreamableRange(offset, limit)
                                ? await selectLinesFromStream(ops.createReadStream(absolutePath, { signal }), startLine, limit, signal)
                                : selectLinesFromBuffer(await ops.readFile(absolutePath), startLine, limit);
                            if (aborted)
                                return;
                            // Check if offset is out of bounds.
                            if (startLine >= totalFileLines) {
                                throw new Error(`Offset ${offset} is beyond end of file (${totalFileLines} lines total)`);
                            }
                            // A user-specified limit is honored before truncation.
                            const userLimitedLines = limit === undefined ? undefined : Math.min(startLine + limit, totalFileLines) - startLine;
                            let outputText;
                            if (truncation.firstLineExceedsLimit) {
                                // First line alone exceeds the byte limit. Point the model at a bash fallback.
                                const firstLineSize = formatSize(firstLineBytes);
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
                            else if (userLimitedLines !== undefined && startLine + userLimitedLines < totalFileLines) {
                                // User-specified limit stopped early, but the file still has more content.
                                const remaining = totalFileLines - (startLine + userLimitedLines);
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
            }).then((result) => ({ ...result, structuredContent: toReadOutput(result.content) }));
        },
        ...readRenderers,
    };
}
export function createReadTool(cwd, options) {
    return wrapToolDefinition(createReadToolDefinition(cwd, options));
}
//# sourceMappingURL=read.js.map