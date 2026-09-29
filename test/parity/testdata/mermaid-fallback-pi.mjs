import { readFileSync } from "node:fs";
import { createMermaidMarkdownTransformer } from "../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/dist/modes/interactive/components/mermaid.js";

// Reuse only the wide input fixture, not native output or expectations. Both engines receive the same diagram that previously triggered the removed fitting search.
const fixture = readFileSync(new URL("../../../internal/codingagent/mermaid_fallback_upstream_test.go", import.meta.url), "utf8");
const wide = fixture.match(/const wideMermaidSource = `([^`]+)`/)[1];
const outputs = [];
for (const [body, width] of [
  ['pie\n  title Pets\n  "Dogs" : 4', 100],
  ["flowchart LR\n  A[Start] --> B[Done]", 10],
  [wide, 80],
  ["flowchart LR\n  A[Foo] invalid\n  B[Bar] also-invalid", 100],
]) {
  const markdown = "```mermaid\n" + body + "\n```";
  for (const isStreaming of [false, true]) {
    outputs.push(createMermaidMarkdownTransformer({ getMode: () => "streaming" })(markdown, {
      availableWidth: width, isStreaming, messageType: "assistant",
    }));
  }
}
console.log(JSON.stringify(outputs));
