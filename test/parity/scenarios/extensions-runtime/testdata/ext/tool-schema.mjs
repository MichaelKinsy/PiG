export default function (pi) {
  const records = [];
  for (const [name, schema] of [["missing", undefined], ["undefined", undefined], ["null", null], ["array", []], ["string", "object"], ["number", 1], ["boolean", false], ["empty-object", {}], ["object", { type: "object", properties: { text: { type: "string" } } }]]) {
    const definition = { name: "noop", label: "No-op", description: "Do nothing", execute: async () => ({ content: [{ type: "text", text: "ok" }] }) };
    if (name !== "missing") definition.parameters = schema;
    try { pi.registerTool(definition); records.push([name, "accepted"]); }
    catch (error) { records.push([name, error.message]); }
  }
  pi.registerCommand("schema-report", { description: "Report schema validation", handler: async (_args, ctx) => ctx.ui.notify(JSON.stringify(records), "info") });
}
