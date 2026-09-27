// Records pi.getAllTools() and pi.getCommands() for parity scenarios.
//
// `/api-info <file>` writes both lists to <file> with object keys sorted, so
// the artifact compares values, not the key order of Node's and Go's JSON
// encoders. The two tools cover a definition with and without prompt
// guidelines; the command covers extension command provenance.
import { writeFileSync } from "node:fs";

const sortedKeys = (value) => {
  if (Array.isArray(value)) return value.map(sortedKeys);
  if (value && typeof value === "object") {
    return Object.fromEntries(Object.keys(value).sort().map((key) => [key, sortedKeys(value[key])]));
  }
  return value;
};

export default function (pi) {
  pi.registerTool({
    name: "api_info_lookup",
    label: "API info lookup",
    description: "Look up one API entry.",
    parameters: {
      type: "object",
      properties: { query: { type: "string", description: "Entry to look up" } },
      required: ["query"],
    },
    promptGuidelines: ["Use api_info_lookup only for API entries."],
    async execute() {
      return { content: [{ type: "text", text: "ok" }], details: {} };
    },
  });
  pi.registerTool({
    name: "api_info_list",
    description: "List API entries.",
    parameters: { type: "object", properties: {} },
    async execute() {
      return { content: [{ type: "text", text: "ok" }], details: {} };
    },
  });
  pi.registerCommand("api-info", {
    description: "Write pi.getAllTools() and pi.getCommands() to a file",
    handler: async (args) => {
      const info = { tools: pi.getAllTools(), commands: pi.getCommands() };
      writeFileSync(args.trim(), `${JSON.stringify(sortedKeys(info), null, 1)}\n`);
    },
  });
}
