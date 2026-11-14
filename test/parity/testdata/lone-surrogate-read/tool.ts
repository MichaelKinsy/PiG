// A tool for the model to call.
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export default function (pi: ExtensionAPI) {
  pi.registerTool({
    name: "echo",
    label: "echo",
    description: "echo",
    parameters: { type: "object", properties: { p: { type: "string" } } },
    async execute() {
      return { content: [{ type: "text", text: "ran" }], details: {} };
    },
  });
}
