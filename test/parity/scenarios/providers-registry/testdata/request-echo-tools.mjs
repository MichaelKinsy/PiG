// Registers tools whose parameter schemas the request-payload pairs read back through the provider fixture's ECHO_REQUEST prompt.
// The schemas are plain JSON Schema objects with their members in a deliberate order, so a request shows what each binary did to them.
const strictPrefer = { type: "json_schema", strict: "prefer" };
const ok = async () => ({ content: [{ type: "text", text: "ok" }] });

export default function requestEchoTools(pi) {
  // Every node has a type: stays strict where the endpoint supports strict tools.
  pi.registerTool({
    name: "typed_tool",
    label: "Typed",
    description: "Every schema node has a type.",
    parameters: { type: "object", properties: { path: { type: "string" } }, required: ["path"] },
    constrainedSampling: strictPrefer,
    execute: ok,
  });
  // An array whose items schema is {}: a node with no type, enum or anyOf (Pi 406e719a6bff).
  pi.registerTool({
    name: "untyped_items_tool",
    label: "Untyped items",
    description: "The items schema is empty.",
    parameters: { type: "object", properties: { commandArguments: { type: "array", items: {} } } },
    constrainedSampling: strictPrefer,
    execute: ok,
  });
  // A bare const is not a type; a bare enum is (Pi 406e719a6bff).
  pi.registerTool({
    name: "bare_const_tool",
    label: "Bare const",
    description: "One property is a bare const.",
    parameters: { type: "object", properties: { mode: { const: "fast" }, level: { enum: ["low", "high"] } }, required: ["mode", "level"] },
    constrainedSampling: strictPrefer,
    execute: ok,
  });
  // Root additionalProperties false, written out of the usual member order, with a root keyword (title) a legacy Anthropic schema drops (Pi aa47cedce).
  pi.registerTool({
    name: "closed_tool",
    label: "Closed",
    description: "No extra arguments.",
    parameters: {
      $defs: { Name: { type: "string" } },
      additionalProperties: false,
      title: "ClosedInput",
      required: ["name"],
      properties: { name: { $ref: "#/$defs/Name" } },
      type: "object",
    },
    execute: ok,
  });
  // Root additionalProperties as a schema (Pi aa47cedce keeps any value).
  pi.registerTool({
    name: "extra_values_tool",
    label: "Extra values",
    description: "Extra arguments are strings.",
    parameters: { type: "object", properties: {}, additionalProperties: { type: "string", description: "extra" } },
    execute: ok,
  });
}
