// Drive the installed pi-tui TuiAltScreen.getWordSelection (tui-alt-screen.ts:1175-1210), never a translated oracle: Intl.Segmenter word segments, visibleWidth columns and the slash/hyphen joining.
// Input: [{line, col}]; output: [[startCol, endCol] or null] for row 0.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { TuiAltScreen } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const screen = Object.create(TuiAltScreen.prototype);
screen.currentLayout = undefined;
const results = JSON.parse(input).map(({ line, col }) => {
	screen.previousScreen = [line];
	const range = screen.getWordSelection({ row: 0, col });
	return range ? [range.start.col, range.end.col] : null;
});
process.stdout.write(JSON.stringify({ node: process.versions.node, icu: process.versions.icu, unicode: process.versions.unicode, results }));
