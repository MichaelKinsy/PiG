import { Type } from "../../../../typebox.mjs";
import { getOrThrow } from "../types.js";
import { DEFAULT_MAX_BYTES, DEFAULT_MAX_LINES, formatSize, truncateHead, } from "../utils/truncate.js";
import { detectSupportedImageMimeType, encodeBase64 } from "./image.js";
import { resolveReadToolPath } from "./path-utils.js";
const readSchema = Type.Object({
    path: Type.String({ description: "Path to the file to read (relative or absolute)" }),
    offset: Type.Optional(Type.Number({ description: "Line number to start reading from (1-indexed)" })),
    limit: Type.Optional(Type.Number({ description: "Maximum number of lines to read" })),
});
export function createReadTool(options) {
    return {
        name: "read",
        label: "read",
        description: `Read the contents of a file. Supports text files and images (jpg, png, gif, webp, bmp). Images are sent as attachments. For text files, output is truncated to ${DEFAULT_MAX_LINES} lines or ${DEFAULT_MAX_BYTES / 1024}KB (whichever is hit first). Use offset/limit for large files. When you need the full file, continue with offset until complete.`,
        parameters: readSchema,
        async execute(_toolCallId, { path, offset, limit }, _onUpdate, { env }, _invocation, context) {
            const absolutePath = await resolveReadToolPath(env, path, context);
            const bytes = getOrThrow(await env.readBinaryFile(absolutePath, context));
            const mimeType = detectSupportedImageMimeType(bytes);
            if (mimeType) {
                if (options?.imageProcessor) {
                    const processed = await options.imageProcessor(bytes, mimeType, { autoResizeImages: options.autoResizeImages ?? true }, context);
                    if (!processed.ok) {
                        return {
                            content: [{ type: "text", text: `Read image file [${mimeType}]\n${processed.message}` }],
                            details: undefined,
                        };
                    }
                    const hints = processed.hints.length > 0 ? `\n${processed.hints.join("\n")}` : "";
                    return {
                        content: [
                            { type: "text", text: `Read image file [${processed.mimeType}]${hints}` },
                            { type: "image", data: processed.data, mimeType: processed.mimeType },
                        ],
                        details: undefined,
                    };
                }
                if (mimeType === "image/bmp") {
                    return {
                        content: [
                            {
                                type: "text",
                                text: "Read image file [image/bmp]\n[Image omitted: configure an imageProcessor to convert BMP images.]",
                            },
                        ],
                        details: undefined,
                    };
                }
                return {
                    content: [
                        { type: "text", text: `Read image file [${mimeType}]` },
                        { type: "image", data: encodeBase64(bytes), mimeType },
                    ],
                    details: undefined,
                };
            }
            const textContent = new TextDecoder().decode(bytes);
            const allLines = textContent.split("\n");
            const totalFileLines = allLines.length;
            const startLine = offset ? Math.max(0, offset - 1) : 0;
            const startLineDisplay = startLine + 1;
            if (startLine >= allLines.length) {
                throw new Error(`Offset ${offset} is beyond end of file (${allLines.length} lines total)`);
            }
            let selectedContent;
            let userLimitedLines;
            if (limit !== undefined) {
                const endLine = Math.min(startLine + limit, allLines.length);
                selectedContent = allLines.slice(startLine, endLine).join("\n");
                userLimitedLines = endLine - startLine;
            }
            else {
                selectedContent = allLines.slice(startLine).join("\n");
            }
            const truncation = truncateHead(selectedContent);
            let outputText;
            let details;
            if (truncation.firstLineExceedsLimit) {
                const firstLineSize = formatSize(new TextEncoder().encode(allLines[startLine]).byteLength);
                outputText = `[Line ${startLineDisplay} is ${firstLineSize}, exceeds ${formatSize(DEFAULT_MAX_BYTES)} limit. Use bash: sed -n '${startLineDisplay}p' ${path} | head -c ${DEFAULT_MAX_BYTES}]`;
                details = { truncation };
            }
            else if (truncation.truncated) {
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
                const remaining = allLines.length - (startLine + userLimitedLines);
                const nextOffset = startLine + userLimitedLines + 1;
                outputText = `${truncation.content}\n\n[${remaining} more lines in file. Use offset=${nextOffset} to continue.]`;
            }
            else {
                outputText = truncation.content;
            }
            return { content: [{ type: "text", text: outputText }], details };
        },
    };
}
//# sourceMappingURL=read.js.map