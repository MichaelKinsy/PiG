// Calls of Pi's normalizeContext that exercise its documented behavior; generate-sdk-vectors.mjs runs Pi's own function over them.
export const cases = [
  { name: "case 0001", args: [{"messages": []}] },
  { name: "case 0002", args: [{"messages": [], "systemPrompt": ""}] },
  { name: "case 0003", args: [{"messages": [], "tools": []}] },
  { name: "case 0004", args: [{"messages": [], "systemPrompt": "sp"}] },
  { name: "case 0005", args: [{"messages": [], "tools": [{"name": "t", "description": "t tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]}] },
  { name: "case 0006", args: [{"systemPrompt": "sp", "tools": [{"name": "t", "description": "t tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}, {"name": "u", "description": "u tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}], "messages": [{"role": "user", "content": "hi", "timestamp": 2}]}] },
  { name: "case 0007", args: [{"systemPrompt": "", "tools": [{"name": "t", "description": "t tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}], "messages": [{"role": "user", "content": "hi", "timestamp": 2}]}] },
  { name: "case 0008", args: [{"tools": [], "messages": [{"role": "user", "content": "hi", "timestamp": 2}]}] },
  { name: "case 0009", args: [{"systemPrompt": "sp", "messages": [{"role": "user", "content": "hi", "timestamp": 2}, {"role": "assistant", "content": [{"type": "text", "text": "a"}], "timestamp": 3}]}] },
];
