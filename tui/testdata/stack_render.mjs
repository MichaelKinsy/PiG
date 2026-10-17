// Drive the installed pi-tui HStack, VStack and ScrollView render, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const { HStack, VStack, ScrollView, Text, setCapabilities } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  const children = (test.children ?? []).map((child) => {
    const component = Array.isArray(child.rows) ? { render: () => child.rows, invalidate() {} } : new Text(child.text, 0, 0);
    const entry = { component };
    for (const key of ["basis", "grow", "shrink", "minSize", "maxSize"]) if (child[key] !== undefined && child[key] !== null) entry[key] = child[key];
    if (child.visibleMinWidth !== undefined && child.visibleMinWidth !== null) entry.visible = (viewport) => viewport.width >= child.visibleMinWidth;
    return entry;
  });
  if (test.kind === "scroll") {
    const view = new ScrollView(children[0].component, { scrollbar: test.scrollbar });
    return test.widths.map((width) => view.render(width));
  }
  const options = {};
  if (test.gap !== null) options.gap = test.gap;
  if (test.align !== "") options.align = test.align;
  const stack = new (test.kind === "h" ? HStack : VStack)(children, options);
  return test.widths.map((width) => stack.render(width));
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
