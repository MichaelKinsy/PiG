// Regenerate with: node internal/codingagent/testdata/gen-mermaid-scope.mjs > /path/to/new-golden.jsonl
// The reviewed snapshot's name/md fields are the input denominator; only the out field is regenerated.
import { readFileSync } from "node:fs";
import { importPiMermaid } from "../../../test/parity/testdata/mermaid-oracle.mjs";

const { createMermaidMarkdownTransformer } = await importPiMermaid();
const transform = createMermaidMarkdownTransformer({ getMode: () => "streaming" });
for (const line of readFileSync(new URL("mermaid-scope-golden.jsonl", import.meta.url), "utf8").trimEnd().split("\n")) {
  const { name, md } = JSON.parse(line);
  console.log(JSON.stringify({ name, md, out: transform(md, { availableWidth: 100, messageType: "assistant", isStreaming: false }) }));
}
