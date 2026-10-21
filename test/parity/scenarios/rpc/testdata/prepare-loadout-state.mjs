// A tool whose prepareLoadout hook reads state the tool changes: its description before the tool has run, and after it a different description and a hidden declaration for the second tool.
let executed = false;
export default function (pi) {
  pi.registerTool({
    name: "watch",
    label: "watch",
    description: "Watch.",
    parameters: { type: "object", properties: {} },
    prepareLoadout: () =>
      executed
        ? { descriptions: { watch: "Watch, after the tool ran." }, hiddenDeclarations: ["other"] }
        : { descriptions: { watch: "Watch, before the tool ran." } },
    async execute() {
      executed = true;
      return { content: [{ type: "text", text: "watched" }], details: {} };
    },
  });
  pi.registerTool({
    name: "other",
    label: "other",
    description: "Other.",
    parameters: { type: "object", properties: {} },
    async execute() {
      return { content: [{ type: "text", text: "other" }], details: {} };
    },
  });
}
