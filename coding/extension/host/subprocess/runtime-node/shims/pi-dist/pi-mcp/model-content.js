/**
 * The model-facing content of an MCP result as pi's coding agent shows it: text and images, embedded text resources
 * as text, binary resources saved where the model can read them, and resource links that name the tool reading them.
 */
import { toLlmContent } from "./protocol/content.js";
/**
 * Model-facing content of `server`'s result, before the output limit ({@link limitMcpContent}). A result without
 * content blocks becomes its structured content as JSON, as in {@link toLlmContent}.
 */
export async function toMcpModelContent(server, result, options = {}) {
    if (result.content.length === 0)
        return toLlmContent(result);
    return (await Promise.all(result.content.map((block) => blockToContent(server, block, options)))).flat();
}
/** Model-facing content of one block of `server`'s result. */
async function blockToContent(server, block, options) {
    if (block.type === "resource_link") {
        const details = [block.mimeType, block.size === undefined ? undefined : formatSize(block.size)].filter(Boolean);
        const read = options.readResourceTool ? `. Read it with ${options.readResourceTool} (server "${server}")` : "";
        const description = block.description ? `: ${block.description}` : "";
        return [
            {
                type: "text",
                text: `[Resource ${block.uri} "${block.title ?? block.name}"${details.length > 0 ? ` (${details.join(", ")})` : ""}${description}${read}]`,
            },
        ];
    }
    if (block.type === "resource" && "blob" in block.resource && !block.resource.mimeType?.startsWith("image/")) {
        const { uri, mimeType, blob } = block.resource;
        const data = Buffer.from(blob, "base64");
        if (isTextMimeType(mimeType))
            return [{ type: "text", text: data.toString("utf8") }];
        if (!options.saveOutput)
            return toLlmContent({ content: [block] });
        const kind = `${mimeType ?? "unknown type"}, ${formatSize(data.length)}`;
        try {
            const path = await options.saveOutput(data, extensionOf(uri));
            return [{ type: "text", text: `[Binary resource ${uri} (${kind}) saved to ${path}]` }];
        }
        catch (error) {
            const reason = error instanceof Error ? error.message : String(error);
            return [{ type: "text", text: `[Binary resource ${uri} (${kind}) could not be saved: ${reason}]` }];
        }
    }
    return toLlmContent({ content: [block] });
}
/** A byte count as pi's coding agent shows sizes: `512B`, `2.0KB`, `1.5MB`. */
function formatSize(bytes) {
    if (bytes < 1024)
        return `${bytes}B`;
    if (bytes < 1024 * 1024)
        return `${(bytes / 1024).toFixed(1)}KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)}MB`;
}
/** Blobs of these types are shown as text. */
function isTextMimeType(mimeType) {
    if (!mimeType)
        return false;
    const type = mimeType.split(";", 1)[0].trim().toLowerCase();
    return type.startsWith("text/") || type === "application/json" || type.endsWith("+json") || type.endsWith("+xml");
}
/** File extension for a saved binary resource: the one its URI ends in, else `.bin`. */
function extensionOf(uri) {
    const path = URL.canParse(uri) ? new URL(uri).pathname : uri;
    return /\.[A-Za-z0-9]{1,8}$/.exec(path)?.[0] ?? ".bin";
}
//# sourceMappingURL=model-content.js.map