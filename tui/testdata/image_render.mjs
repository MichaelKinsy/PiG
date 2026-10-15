// Drive the installed pi-tui Image component through renders at several widths (and an invalidate), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { Image, setCapabilities, setCellDimensions } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
Math.random = () => 0.5; // the kitty image id allocated on first render
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  setCapabilities({ images: probe.protocol || null, trueColor: true, hyperlinks: false });
  setCellDimensions({ widthPx: probe.cellWidth, heightPx: probe.cellHeight });
  const options = {};
  if (probe.maxWidthCells !== null) options.maxWidthCells = probe.maxWidthCells;
  if (probe.maxHeightCells !== null) options.maxHeightCells = probe.maxHeightCells;
  if (probe.filename) options.filename = probe.filename;
  if (probe.imageId !== null) options.imageId = probe.imageId;
  const dims = probe.dims ? { widthPx: probe.dims[0], heightPx: probe.dims[1] } : undefined;
  const image = new Image(probe.data, probe.mime, { fallbackColor: (s) => `<fb>${s}</fb>` }, options, dims);
  const out = [];
  for (const step of probe.steps) {
    if (step === "invalidate") { image.invalidate(); out.push(null); } else out.push(image.render(step));
  }
  return { frames: out, id: image.getImageId() ?? null };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
