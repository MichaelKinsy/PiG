// Calls of Pi's getModelType that exercise its documented behavior; generate-sdk-vectors.mjs runs Pi's own function over them.
export const cases = [
  { name: "case 0001", args: [{"id": "a", "provider": "p", "api": "x"}] },
  { name: "case 0002", args: [{"id": "a", "provider": "p", "api": "x", "type": "chat"}] },
  { name: "case 0003", args: [{"id": "a", "provider": "p", "api": "x", "type": "image"}] },
  { name: "case 0004", args: [{"id": "a", "provider": "p", "api": "y", "type": "classifier"}] },
  { name: "case 0005", args: [{"id": "b", "provider": "p", "api": "x"}] },
  { name: "case 0006", args: [{"id": "a", "provider": "q", "api": "x"}] },
  { name: "case 0007", args: [{}] },
];
