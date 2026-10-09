// Drive the installed pi-tui Loader and CancellableLoader constructors, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
globalThis.setInterval = () => 0;
globalThis.clearInterval = () => {};
const { Loader, CancellableLoader } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((test) => {
  const Class = test.cancellable ? CancellableLoader : Loader;
  const identity = (text) => text;
  const loader = new Class({ requestRender() {} }, identity, identity, test.message, test.indicator ?? undefined);
  return { frames: loader.frames, intervalMs: loader.intervalMs, verbatim: loader.renderIndicatorVerbatim, rows: [30, 4].map((width) => loader.render(width)) };
});
process.stdout.write(JSON.stringify(results));
