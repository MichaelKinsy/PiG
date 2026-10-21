// Calls of Pi's StringEnum that exercise its documented behavior; generate-sdk-vectors.mjs runs Pi's own function over them.
export const cases = [
  { name: "case 0001", args: [["a", "b"]] },
  { name: "case 0002", args: [["a", "b"], {}] },
  { name: "case 0003", args: [["a", "b"], {"description": "d"}] },
  { name: "case 0004", args: [["a", "b"], {"default": "a"}] },
  { name: "case 0005", args: [["a", "b"], {"description": "d", "default": "b"}] },
  { name: "case 0006", args: [["a", "b"], {"description": "", "default": ""}] },
  { name: "case 0007", args: [[], {"description": "none"}] },
  { name: "case 0008", args: [["x y", "é", "😀", "\"q\""], {"description": "uni code"}] },
];
