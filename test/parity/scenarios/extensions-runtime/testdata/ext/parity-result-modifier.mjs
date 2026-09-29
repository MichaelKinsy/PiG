// parity-result-modifier: registers a tool_result handler that appends
// a marker suffix to any tool result content. Exercises runner.ts result pipeline.
export default function (pi) {
  pi.on("tool_result", (event, ctx) => {
    if (event.toolName === "bash") {
      // Append a marker to content to prove we intercept results.
      const content = event.content || [];
      const newContent = content.map((item) => {
        if (item && item.type === "text") {
          return { ...item, text: item.text + "\n[parity-modified]" };
        }
        return item;
      });
      return { content: newContent };
    }
  });
}
