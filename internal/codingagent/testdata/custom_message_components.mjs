// Drive the installed Pi's custom message and custom entry components, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setKeybindings, setCapabilities, Text } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { KeybindingsManager } = await load("dist/core/keybindings.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { CustomMessageComponent } = await load("dist/modes/interactive/components/custom-message.js");
const { CustomEntryComponent } = await load("dist/modes/interactive/components/custom-entry.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const renderer = (mode) => {
  if (mode === "none") return undefined;
  return (_value, options) => {
    if (mode === "throw") throw new Error("boom");
    if (mode === "undefined") return undefined;
    return new Text(`custom expanded=${options.expanded} pad=${options.outputPad ?? "-"}`, 0, 0);
  };
};
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme("dark");
  setKeybindings(new KeybindingsManager({}));
  const frames = [];
  if (test.kind === "message") {
    const component = new CustomMessageComponent({ role: "custom", customType: "note", content: test.content, display: true, timestamp: 0 }, renderer(test.renderer), undefined, test.pad);
    for (const [expanded, pad] of [[false, test.pad], [true, test.pad], [true, test.pad2], [false, test.pad2]]) {
      component.setExpanded(expanded);
      component.setOutputPad(pad);
      frames.push(component.render(test.width));
    }
    return { frames, hasContent: false };
  }
  const entryRenderer = renderer(test.renderer) ?? (() => undefined);
  const component = new CustomEntryComponent({ type: "custom", id: "1", parentId: null, timestamp: "", customType: "note", data: {} }, (entry, options) => entryRenderer(entry, { expanded: options.expanded }), test.pad);
  for (const [expanded, pad] of [[false, test.pad], [true, test.pad], [true, test.pad2]]) {
    component.setExpanded(expanded);
    component.setOutputPad(pad);
    frames.push(component.render(test.width));
  }
  return { frames, hasContent: component.hasContent() };
});
process.stdout.write(JSON.stringify(results));
