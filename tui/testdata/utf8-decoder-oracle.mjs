// Generates utf8-decoder-oracle.json: chunk sequences run through Node's StringDecoder, which Pi's ProcessTerminal reaches through process.stdin.setEncoding("utf8").
// Usage: node utf8-decoder-oracle.mjs > utf8-decoder-oracle.json
// A case is {chunks: [hex...], out: [string per write]}. The classes cover every trailing lead byte C0..FF, E0/ED/F0/F4 with an out-of-range second byte, a held byte followed by a non-continuation, and complete or overlong runs.
import { StringDecoder } from "node:string_decoder";

const lead = [0xc0, 0xc1, 0xc2, 0xdf, 0xe0, 0xe1, 0xed, 0xee, 0xf0, 0xf1, 0xf4, 0xf5, 0xf7, 0xf8, 0xff, 0x80, 0xbf];
const second = [0x80, 0x8f, 0x90, 0x9f, 0xa0, 0xbf, 0x41, 0xc3];
const chunkSets = [];
const add = (...c) => chunkSets.push(c);
for (const a of lead) {
  add([0x61, a]);
  add([a], [0x41]);
  add([a], [0x80]);
  add([a], [0x80, 0x80], [0x80]);
  add([0x60, 0x20, a], [0x42]);
  for (const b of second) {
    add([0x61, a, b]);
    add([a, b], [0x80]);
    add([a, b], [0x80, 0x80]);
    add([a, b], [0x41]);
    add([a, b, 0x80], [0x80]);
    add([a], [b], [0x80], [0x80]);
  }
}
add([0xf0, 0x9f, 0x98], [0x80]);
add([0xf0, 0x9f], [0x98, 0x80, 0xe4, 0xb8], [0xad]);
add([0xe4, 0xb8, 0xad]);
add([0xc3], [0xa9, 0xc3], [0xa9]);
add([0xe2, 0x82], [0x41, 0xe2], [0x82, 0xac]);
add([0x80, 0x80, 0x80, 0x80, 0x80]);
add([0xf0, 0x80, 0x80, 0x80, 0x80]);
add([]);
add([], [0xc3], []);
const hex = (b) => Buffer.from(b).toString("hex");
const cases = chunkSets.map((chunks) => {
  const d = new StringDecoder("utf8");
  return { chunks: chunks.map(hex), out: chunks.map((c) => d.write(Buffer.from(c))) };
});
process.stdout.write(JSON.stringify(cases) + "\n");
