// Drive the installed pi-tui renderImage (terminal-image.ts) through sequences of calls in one module state and record what it registers: the placement
// getKittyImagePlacement derives from each result's sequence, and getMaxKittyImageRows, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { renderImage, setCapabilities, setCellDimensions, getKittyImagePlacement, getMaxKittyImageRows } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/terminal-image.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  setCapabilities({ images: probe.protocol || null, trueColor: true, hyperlinks: false });
  setCellDimensions({ widthPx: probe.cellWidth, heightPx: probe.cellHeight });
  const options = {};
  if (probe.maxWidthCells !== null) options.maxWidthCells = probe.maxWidthCells;
  if (probe.maxHeightCells !== null) options.maxHeightCells = probe.maxHeightCells;
  if (probe.imageId !== null) options.imageId = probe.imageId;
  const result = renderImage(probe.data, { widthPx: probe.widthPx, heightPx: probe.heightPx }, options);
  const placement = result === null ? null : getKittyImagePlacement(result.sequence) ?? null;
  return { rendered: result !== null, placement, maxRows: getMaxKittyImageRows() };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
