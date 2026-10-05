#!/usr/bin/env node
// Probe the pinned renderers' numeric warning text with an unstyled fixture theme. Layout and theme escapes are covered by the existing full-render tests, not this numeric crop.
import assert from "node:assert/strict";
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";

const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url));
assert.equal(JSON.parse(readFileSync(`${root}/package.json`, "utf8")).version, "1.0.2");
const observations = [];
for (const [name, field] of [["grep", "matchLimitReached"], ["find", "resultLimitReached"], ["ls", "entryLimitReached"]]) {
  const module = await import(pathToFileURL(`${root}/dist/core/tools/renderers/${name}.js`));
  for (const value of [1e-7, 1e21]) {
    const component = module[`${name}Renderers`].renderResult(
      { content: [{ type: "text", text: "hit" }], details: { [field]: value } },
      { expanded: false, isPartial: false },
      { fg: (_role, text) => text },
      { showImages: false },
    );
    const marker = "[Truncated: ";
    const row = component.render(80).find(row => row.includes(marker));
    assert.ok(row, `${name} did not render the warning`);
    const start = row.indexOf(marker) + marker.length;
    observations.push(row.slice(start, row.indexOf("]", start)));
  }
}
process.stdout.write(`list-limit:${JSON.stringify(observations)}\n`);
