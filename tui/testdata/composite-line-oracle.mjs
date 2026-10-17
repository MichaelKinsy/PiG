// Generates composite-line-oracle.json: seeded random compositeTuiLine inputs run through Pi's tui.js.
// Usage: node composite-line-oracle.mjs <path to @earendil-works/pi-tui/dist/index.js> > composite-line-oracle.json
import { pathToFileURL } from "node:url";
const { compositeTuiLine } = await import(pathToFileURL(process.argv[2]).href);
let seed = 0xc0ffee01;
const random = () => {
	seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
	return seed / 0x100000000;
};
const pick = (list) => list[Math.floor(random() * list.length)];
const atoms = ["a", "b", " ", "hello", "日本", "😀", "e\u0301", "\x1b[31m", "\x1b[0m", "\x1b[1;4m", "\x1b[38;2;1;2;3m", "\x1b[39m", "\x1b]8;;http://x\x07", "\x1b]8;;\x07", "\x1b_pi:c\x07", "ก็", "-", "\t", "│", "\x1b[7m \x1b[0m", "\u{1F1FA}\u{1F1F8}"];
const build = () => Array.from({ length: Math.floor(random() * 9) }, () => pick(atoms)).join("");
const image = ["\x1b_Gf=100,a=T;AAAA\x1b\\", "\x1b]1337;File=inline=1:AAAA\x07"];
const cases = [];
for (let i = 0; i < 1500; i++) {
	const totalWidth = pick([0, 1, 5, 10, 20, 40, 80]);
	const startCol = pick([0, 0, 1, 2, 5, 9, 15, 30]);
	const overlayWidth = pick([0, 1, 3, 6, 12, 25]);
	const baseLine = random() < 0.03 ? pick(image) : build();
	const overlayLine = build();
	cases.push({ baseLine, overlayLine, startCol, overlayWidth, totalWidth, out: compositeTuiLine(baseLine, overlayLine, startCol, overlayWidth, totalWidth) });
}
process.stdout.write(JSON.stringify(cases) + "\n");
