// Run from the repository root. These source-only probes never install dependencies.
import assert from "node:assert/strict";
import { detectSupportedImageMimeType as detect } from "../../../.upstream/current/packages/agent/src/harness/tools/image.ts";
import { JSONL_FORMAT_VERSION, JSONL_STORAGE_VERSION } from "../../../.upstream/current/packages/agent/src/harness/session/jsonl/types.ts";
import { contentText, getSystemMessageText, renderSystemMessageUpdate } from "../../../.upstream/current/packages/ai/src/utils/text.ts";

const png = [0x89, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82];
const ihdr = [...png, ...Array(17).fill(0)];
const chunk = (base, name) => [...base, 0, 0, 0, 0, ...Buffer.from(name), 0, 0, 0, 0];
for (const [name, bytes, expected] of [
  ["jpeg-prefix-only", [255, 216, 255], "image/jpeg"],
  ["jpeg-ls", [255, 216, 255, 247], undefined],
  ["png-minimal-ihdr", png, "image/png"],
  ["png-actl-before-idat", chunk(chunk(ihdr, "acTL"), "IDAT"), undefined],
  ["png-actl-after-idat", chunk(chunk(ihdr, "IDAT"), "acTL"), "image/png"],
  ["png-truncated-chunk", [...ihdr, 0, 0, 0, 8, ...Buffer.from("tEXt")], "image/png"],
  ["png-overflow-length", [...ihdr, 255, 255, 255, 255, ...Buffer.from("tEXt")], "image/png"],
]) {
  const actual = detect(Uint8Array.from(bytes));
  assert.equal(actual, expected, name);
  console.log(JSON.stringify({ name, actual: actual ?? null }));
}
assert.equal(JSONL_FORMAT_VERSION, 4);
assert.equal(JSONL_STORAGE_VERSION, 1);
console.log(JSON.stringify({ JSONL_FORMAT_VERSION, JSONL_STORAGE_VERSION }));

const mixed = [
  { type: "thinking", thinking: "hidden" },
  { type: "text", text: "first" },
  { type: "toolCall", id: "1", name: "read", arguments: {} },
  { type: "image", data: "...", mimeType: "image/png" },
  { type: "text", text: "second" },
];
assert.equal(contentText(mixed), "first\nsecond");
assert.equal(contentText(mixed, ""), "firstsecond");
assert.equal(contentText("hello"), "hello");
assert.equal(contentText([]), "");
console.log(JSON.stringify({ name: "contentText", default: contentText(mixed), custom: contentText(mixed, "") }));
const blocks = { role: "system", content: [{ type: "text", text: "first" }, { type: "text", text: "second" }], timestamp: 0 };
const empty = { role: "system", content: "base", sections: { empty: "", removed: null, last: "last" }, timestamp: 0 };
const literal = { role: "system", content: "", sections: { 'a"b': "new", "x\ny": null }, timestamp: 0 };
assert.equal(renderSystemMessageUpdate(blocks), "first\nsecond");
assert.equal(getSystemMessageText(empty), "base\n\nlast");
assert.equal(renderSystemMessageUpdate(literal), 'Updated system prompt section "a"b":\n\nnew\n\nRemoved system prompt section "x\ny".');
for (const [name, actual] of [
  ["system-block-separator", renderSystemMessageUpdate(blocks)],
  ["empty-system-section", getSystemMessageText(empty)],
  ["literal-section-name", renderSystemMessageUpdate(literal)],
]) console.log(JSON.stringify({ name, actual }));
