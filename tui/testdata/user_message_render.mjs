// Drive the installed Pi UserMessageComponent, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
const { UserMessageComponent } = await load("dist/modes/interactive/components/user-message.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  initTheme(test.theme);
  const component = new UserMessageComponent(test.text, undefined, test.pad);
  const frames = [test.widths.map((w) => component.render(w))];
  for (const pad of test.pads) {
    component.setOutputPad(pad);
    frames.push(test.widths.map((w) => component.render(w)));
  }
  return frames;
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
