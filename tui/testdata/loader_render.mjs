// Drive the installed pi-tui Loader through frames and message changes, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
globalThis.setInterval = () => 0;
globalThis.clearInterval = () => {};
const { Loader } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const tag = (name) => (text) => `<${name}>${text}</${name}>`;
const results = JSON.parse(input).map((test) => {
  const loader = new Loader({ requestRender() {} }, tag("sp"), tag("msg"), test.message, test.indicator ?? undefined);
  const frames = [];
  for (const step of test.steps) {
    if (step.message !== undefined && step.message !== null) loader.setMessage(step.message);
    for (let i = 0; i < step.ticks; i++) {
      loader.currentFrame = (loader.currentFrame + 1) % Math.max(1, loader.frames.length);
      loader.updateDisplay();
    }
    frames.push(test.widths.map((width) => loader.render(width)));
  }
  return frames;
});
process.stdout.write(JSON.stringify(results));
