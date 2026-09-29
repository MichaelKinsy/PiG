export function createMarkdownTransform(messageType, isStreaming, transformers) {
    return (markdown, availableWidth) => applyMarkdownTransformers(markdown, { messageType, isStreaming, availableWidth }, transformers);
}
function applyMarkdownTransformers(markdown, context, transformers) {
    let transformedMarkdown = markdown;
    for (const transformer of transformers) {
        try {
            const transformed = transformer(transformedMarkdown, context);
            if (typeof transformed === "string") {
                transformedMarkdown = transformed;
            }
        }
        catch {
            // Keep the current Markdown and continue with the next transformer.
        }
    }
    return transformedMarkdown;
}
//# sourceMappingURL=markdown-transform.js.map