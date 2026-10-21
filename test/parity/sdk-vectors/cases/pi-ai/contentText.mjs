// Calls of Pi's contentText that exercise its documented behavior; generate-sdk-vectors.mjs runs Pi's own function over them.
export const cases = [
  { name: "case 0001", args: ["plain"] },
  { name: "case 0002", args: ["plain", " | "] },
  { name: "case 0003", args: ["plain", ""] },
  { name: "case 0004", args: ["plain", " "] },
  { name: "case 0005", args: [""] },
  { name: "case 0006", args: ["", " | "] },
  { name: "case 0007", args: ["", ""] },
  { name: "case 0008", args: ["", " "] },
  { name: "case 0009", args: [[]] },
  { name: "case 0010", args: [[], " | "] },
  { name: "case 0011", args: [[], ""] },
  { name: "case 0012", args: [[], " "] },
  { name: "case 0013", args: [[{"type": "text", "text": "a"}]] },
  { name: "case 0014", args: [[{"type": "text", "text": "a"}], " | "] },
  { name: "case 0015", args: [[{"type": "text", "text": "a"}], ""] },
  { name: "case 0016", args: [[{"type": "text", "text": "a"}], " "] },
  { name: "case 0017", args: [[{"type": "text", "text": "a"}, {"type": "text", "text": "b"}]] },
  { name: "case 0018", args: [[{"type": "text", "text": "a"}, {"type": "text", "text": "b"}], " | "] },
  { name: "case 0019", args: [[{"type": "text", "text": "a"}, {"type": "text", "text": "b"}], ""] },
  { name: "case 0020", args: [[{"type": "text", "text": "a"}, {"type": "text", "text": "b"}], " "] },
  { name: "case 0021", args: [[{"type": "text", "text": "a"}, {"type": "image", "data": "x", "mimeType": "image/png"}, {"type": "text", "text": "b"}]] },
  { name: "case 0022", args: [[{"type": "text", "text": "a"}, {"type": "image", "data": "x", "mimeType": "image/png"}, {"type": "text", "text": "b"}], " | "] },
  { name: "case 0023", args: [[{"type": "text", "text": "a"}, {"type": "image", "data": "x", "mimeType": "image/png"}, {"type": "text", "text": "b"}], ""] },
  { name: "case 0024", args: [[{"type": "text", "text": "a"}, {"type": "image", "data": "x", "mimeType": "image/png"}, {"type": "text", "text": "b"}], " "] },
  { name: "case 0025", args: [[{"type": "thinking", "thinking": "t"}, {"type": "toolCall", "id": "1", "name": "n", "arguments": {}}]] },
  { name: "case 0026", args: [[{"type": "thinking", "thinking": "t"}, {"type": "toolCall", "id": "1", "name": "n", "arguments": {}}], " | "] },
  { name: "case 0027", args: [[{"type": "thinking", "thinking": "t"}, {"type": "toolCall", "id": "1", "name": "n", "arguments": {}}], ""] },
  { name: "case 0028", args: [[{"type": "thinking", "thinking": "t"}, {"type": "toolCall", "id": "1", "name": "n", "arguments": {}}], " "] },
  { name: "case 0029", args: [[{"type": "text", "text": ""}, {"type": "text", "text": "b"}, {"type": "text", "text": ""}]] },
  { name: "case 0030", args: [[{"type": "text", "text": ""}, {"type": "text", "text": "b"}, {"type": "text", "text": ""}], " | "] },
  { name: "case 0031", args: [[{"type": "text", "text": ""}, {"type": "text", "text": "b"}, {"type": "text", "text": ""}], ""] },
  { name: "case 0032", args: [[{"type": "text", "text": ""}, {"type": "text", "text": "b"}, {"type": "text", "text": ""}], " "] },
  { name: "case 0033", args: [[{"type": "text", "text": "line1\nline2"}, {"type": "text", "text": "😀"}]] },
  { name: "case 0034", args: [[{"type": "text", "text": "line1\nline2"}, {"type": "text", "text": "😀"}], " | "] },
  { name: "case 0035", args: [[{"type": "text", "text": "line1\nline2"}, {"type": "text", "text": "😀"}], ""] },
  { name: "case 0036", args: [[{"type": "text", "text": "line1\nline2"}, {"type": "text", "text": "😀"}], " "] },
];
