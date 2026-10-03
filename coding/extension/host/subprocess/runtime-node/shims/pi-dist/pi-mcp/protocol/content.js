function blockToLlmContent(block) {
    switch (block.type) {
        case "text":
            return { type: "text", text: block.text };
        case "image":
            return { type: "image", data: block.data, mimeType: block.mimeType };
        case "audio":
            return { type: "text", text: `[audio ${block.mimeType} omitted]` };
        case "resource_link":
            return { type: "text", text: `${block.name}: ${block.uri}` };
        case "resource": {
            const resource = block.resource;
            if ("text" in resource)
                return { type: "text", text: resource.text };
            if (resource.mimeType?.startsWith("image/")) {
                return { type: "image", data: resource.blob, mimeType: resource.mimeType };
            }
            return {
                type: "text",
                text: `[binary resource ${resource.uri} (${resource.mimeType ?? "unknown type"}) omitted]`,
            };
        }
        default:
            return { type: "text", text: `[unsupported MCP content ${block.type}]` };
    }
}
/**
 * Convert a tool result to text and image content for a model. Text and images pass through,
 * embedded text resources become text, embedded image resources become images, and other blocks
 * (audio, resource links, binary resources) become a short text placeholder. A result without
 * content blocks but with `structuredContent` becomes its JSON, since servers should, but do not
 * always, mirror structured results as text.
 */
export function toLlmContent(result) {
    const content = (result.content ?? []).map(blockToLlmContent);
    if (content.length === 0 && result.structuredContent !== undefined) {
        content.push({ type: "text", text: JSON.stringify(result.structuredContent, null, 2) });
    }
    return content;
}
//# sourceMappingURL=content.js.map