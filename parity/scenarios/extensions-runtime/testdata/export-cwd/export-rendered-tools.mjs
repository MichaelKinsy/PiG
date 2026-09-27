// Prints the renderedTools of an HTML export as one sorted JSON line, the
// tool call ids replaced by their order of appearance.
import { readFileSync } from "node:fs";
const html = readFileSync(process.argv[2], "utf8");
const match = html.match(/<script id="session-data" type="application\/json">([^<]*)<\/script>/);
const data = JSON.parse(Buffer.from(match[1], "base64").toString("utf8"));
const rendered = data.renderedTools ?? {};
const ids = Object.keys(rendered);
const order = new Map();
for (const entry of data.entries) {
  for (const block of entry.message?.content ?? []) {
    if (block.type === "toolCall" && !order.has(block.id)) order.set(block.id, `${block.name}#${order.size}`);
  }
}
const out = {};
for (const id of ids.sort((a, b) => (order.get(a) ?? a).localeCompare(order.get(b) ?? b))) out[order.get(id) ?? id] = rendered[id];
console.log("RENDERED " + JSON.stringify(out));
