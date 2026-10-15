// Minimal Node Pig extension: Pi's default-export factory, registering one tool.
// The Node runtime supplies "@earendil-works/pi-coding-agent" itself, so the file needs no install step.
export default function (pi) {
  pi.registerTool({
    name: "hello",
    label: "Hello",
    description: "Return a hello greeting",
    parameters: { type: "object", properties: {} },
    async execute() {
      return { content: [{ type: "text", text: "hello from node" }], details: {} };
    },
  });
  // A tool with a parameter: the schema tells the model what to pass, execute checks it, and a throw is the tool call's error result.
  pi.registerTool({
    name: "count_words",
    label: "Count words",
    description: "Count the words in a text",
    parameters: {
      type: "object",
      properties: { text: { type: "string", description: "The text to count" } },
      required: ["text"],
    },
    async execute(_id, params) {
      if (typeof params.text !== "string" || params.text === "") throw new Error("text is required");
      return { content: [{ type: "text", text: `${(params.text.match(/\S+/g) ?? []).length} words` }], details: {} };
    },
  });
}
