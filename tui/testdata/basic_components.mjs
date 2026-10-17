// Drive the installed pi-tui Text, TruncatedText, Spacer and Box, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { Text, TruncatedText, Spacer, Box } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const bg = (text) => `<bg>${text}</bg>`;
const build = (spec) => {
  switch (spec.kind) {
    case "text": return new Text(spec.text, spec.paddingX, spec.paddingY, spec.bg ? bg : undefined);
    case "truncated": return new TruncatedText(spec.text, spec.paddingX, spec.paddingY);
    case "spacer": return new Spacer(spec.lines);
    case "box": {
      const box = new Box(spec.paddingX, spec.paddingY, spec.bg ? bg : undefined);
      for (const child of spec.children ?? []) box.addChild(build(child));
      return box;
    }
  }
  throw new Error("unknown kind " + spec.kind);
};
const results = JSON.parse(input).map((probe) => {
  const component = build(probe.spec);
  const first = component.render(probe.width);
  // A second render, as a cached frame is shown, then after invalidate.
  const second = component.render(probe.width);
  component.invalidate();
  const third = component.render(probe.width);
  return { first, second, third };
});
process.stdout.write(JSON.stringify(results));
