// Runs the installed, pinned Pi implementation of the system theme generator over the cases on stdin.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
const version = JSON.parse(readFileSync(root + "package.json", "utf8")).version;
if (version !== process.argv[2]) throw new Error(`Pi oracle ${version}, expected ${process.argv[2]}`);
const { generateSystemThemeColors } = await import(pathToFileURL(root + "dist/modes/interactive/theme/system-theme.js"));
const cases = JSON.parse(readFileSync(process.argv[3], "utf8"));
const results = cases.map((input) => {
  const generated = generateSystemThemeColors(input);
  return { colors: generated.colors, dim: generated.dim ?? [], appearance: generated.appearance ?? "" };
});
process.stdout.write(JSON.stringify(results));
