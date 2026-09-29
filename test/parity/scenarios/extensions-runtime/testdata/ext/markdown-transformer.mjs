// markdown-transformer: registers a Markdown transformer that rewrites user
// and assistant Markdown for display, naming the message type it was given.
export default function (pi) {
  pi.registerMarkdownTransformer((markdown, context) => {
    if (context.messageType === "user") return markdown.replace("20+22", "twenty plus twenty-two");
    if (context.messageType === "assistant" && !context.isStreaming) return markdown.replace(/^42$/m, "**forty-two** from the transformer");
    return markdown;
  });
}
