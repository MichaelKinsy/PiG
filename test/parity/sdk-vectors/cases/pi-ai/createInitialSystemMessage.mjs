// Calls of Pi's createInitialSystemMessage that exercise its documented behavior; generate-sdk-vectors.mjs runs Pi's own function over them.
export const cases = [
  { name: "case 0001", args: [undefined, undefined] },
  { name: "case 0002", args: ["", []] },
  { name: "case 0003", args: ["p", undefined] },
  { name: "case 0004", args: [undefined, [{"name": "t", "description": "t tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]] },
  { name: "case 0005", args: ["p", [{"name": "t", "description": "t tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]] },
  { name: "case 0006", args: ["", [{"name": "t", "description": "t tool", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}]] },
  { name: "case 0007", args: ["p", []] },
];
