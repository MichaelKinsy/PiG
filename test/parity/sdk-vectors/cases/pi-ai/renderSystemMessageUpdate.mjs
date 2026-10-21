// Calls of Pi's renderSystemMessageUpdate that exercise its documented behavior; generate-sdk-vectors.mjs runs Pi's own function over them.
export const cases = [
  { name: "case 0001", args: [{"role": "system", "content": "prompt", "timestamp": 1}] },
  { name: "case 0002", args: [{"role": "system", "content": "", "timestamp": 1}] },
  { name: "case 0003", args: [{"role": "system", "content": [{"type": "text", "text": "a"}, {"type": "text", "text": "b"}], "timestamp": 1}] },
  { name: "case 0004", args: [{"role": "system", "content": "prompt", "timestamp": 1, "sections": {"a": "A", "b": "B"}}] },
  { name: "case 0005", args: [{"role": "system", "content": "prompt", "timestamp": 1, "sections": {"a": "A", "b": null, "c": ""}}] },
  { name: "case 0006", args: [{"role": "system", "content": "", "timestamp": 1, "sections": {"only": "S"}}] },
  { name: "case 0007", args: [{"role": "system", "content": "", "timestamp": 1, "sections": {"only": null}}] },
  { name: "case 0008", args: [{"role": "system", "content": "p", "timestamp": 1, "sections": {"1": "one", "2": "two", "b": "B"}}] },
  { name: "case 0009", args: [{"role": "system", "content": "p", "timestamp": 1, "sections": {}}] },
];
