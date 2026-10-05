// Regenerate with: node internal/codingagent/testdata/gen-mermaid-scope.mjs > /path/to/new-golden.jsonl
// The reviewed snapshot's name/md fields are the input denominator; only the out field is regenerated.
import assert from "node:assert/strict";
import { readFileSync, realpathSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.env.PI_PACKAGE_ROOT ?? realpathSync("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.0.3");
const { createMermaidMarkdownTransformer } = await import(pathToFileURL(join(root, "dist/modes/interactive/components/mermaid.js")));
const transform = createMermaidMarkdownTransformer({ getMode: () => "streaming" });
for (const line of readFileSync(new URL("mermaid-scope-golden.jsonl", import.meta.url), "utf8").trimEnd().split("\n")) {
  const { name, md } = JSON.parse(line);
  console.log(JSON.stringify({ name, md, out: transform(md, { availableWidth: 100, messageType: "assistant", isStreaming: false }) }));
}
