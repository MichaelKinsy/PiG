// Generates colors-oracle.json from upstream packages/tui/src/colors.ts (0.99.1).
// Usage: node colors-oracle.mjs <path to packages/tui/src> > colors-oracle.json
import { pathToFileURL } from "node:url";
import { join } from "node:path";

const src = process.argv[2];
if (!src) throw new Error("usage: node colors-oracle.mjs <packages/tui/src>");
const colors = await import(pathToFileURL(join(src, "colors.ts")).href);

let seed = 0x2545f491;
const random = () => {
	seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
	return seed / 0x100000000;
};
const hex = () => `#${Array.from({ length: 3 }, () => Math.floor(random() * 256).toString(16).padStart(2, "0")).join("")}`;

const oklchToRgb = [];
for (let i = 0; i < 600; i++) {
	const l = i < 3 ? [0, 1, 0.5][i] : random();
	const c = random() * 0.45;
	const h = random() * 720 - 180;
	oklchToRgb.push({ l, c, h, rgb: colors.colorToRgb(colors.oklchColor(l, c, h)) });
}
const okhslToRgb = [];
for (let i = 0; i < 600; i++) {
	const h = random() * 900 - 270;
	const s = i % 7 === 0 ? 1 : i % 11 === 0 ? 0 : random();
	const l = i % 13 === 0 ? [0, 1][i % 2] : random();
	okhslToRgb.push({ h, s, l, rgb: colors.colorToRgb(colors.okhslColor(h, s, l)) });
}
const hexToOkhsl = [];
const hexToOklch = [];
for (let i = 0; i < 600; i++) {
	const value = i < 4 ? ["#000000", "#ffffff", "#808080", "#ff0000"][i] : hex();
	hexToOkhsl.push({ hex: value, okhsl: colors.colorToOkhsl(colors.parseColor(value)) });
	hexToOklch.push({ hex: value, oklch: colors.colorToOklch(colors.parseColor(value)) });
}
const mixes = [];
for (let i = 0; i < 400; i++) {
	const first = hex();
	const second = hex();
	const amount = random();
	const space = i % 2 === 0 ? "oklch" : "srgb";
	const mixed = colors.mixColors(colors.parseColor(first), colors.parseColor(second), amount, space);
	mixes.push({ first, second, amount, space, hex: colors.colorToHex(mixed), rgb: colors.colorToRgb(mixed) });
}
const ansi = [];
for (let i = 0; i < 600; i++) {
	const value = hex();
	const color = colors.parseColor(value);
	ansi.push({ hex: value, fg256: colors.foregroundAnsi(color, "256color"), bg256: colors.backgroundAnsi(color, "256color"), fgTrue: colors.foregroundAnsi(color, "truecolor") });
}
console.log(JSON.stringify({ oklchToRgb, okhslToRgb, hexToOkhsl, hexToOklch, mixes, ansi }));
