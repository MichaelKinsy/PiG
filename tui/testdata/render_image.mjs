// Drive the installed pi-tui renderImage (terminal-image.ts) directly, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { renderImage, setCapabilities, setCellDimensions } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  setCapabilities({ images: probe.protocol || null, trueColor: true, hyperlinks: false });
  setCellDimensions({ widthPx: probe.cellWidth, heightPx: probe.cellHeight });
  const options = {};
  if (probe.maxWidthCells !== null) options.maxWidthCells = probe.maxWidthCells;
  if (probe.maxHeightCells !== null) options.maxHeightCells = probe.maxHeightCells;
  if (probe.preserveAspectRatio !== null) options.preserveAspectRatio = probe.preserveAspectRatio;
  if (probe.imageId !== null) options.imageId = probe.imageId;
  if (probe.moveCursor !== null) options.moveCursor = probe.moveCursor;
  const result = renderImage(probe.data, { widthPx: probe.widthPx, heightPx: probe.heightPx }, options);
  return result === null ? null : { sequence: result.sequence, columns: result.columns, rows: result.rows, imageId: result.imageId ?? null };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
