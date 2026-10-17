// Generates colors-parse-oracle.json: parseColor/okhslColor/oklchColor accept and reject sets, run through Pi's colors.js.
// Usage: node colors-parse-oracle.mjs <path to @earendil-works/pi-tui/dist/index.js> > colors-parse-oracle.json
import { pathToFileURL } from "node:url";
const tui = await import(pathToFileURL(process.argv[2]).href);
const nbsp = "\u00a0", ls = "\u2028", bom = "\ufeff", ideo = "\u3000", nel = "\u0085", vt = "\u000b";
const inputs = [
	"#fff", "#FFF", "#ffffff", "#FfA0b1", "#ffff", "#ff", "#gggggg", "# fff", "#fff ", " #fff", "fff",
	"oklch(50% 0.1 120)", "oklch(0.5 0.1 120)", "OKLCH(0.5 0.1 120deg)", "oklch( 0.5   0.1   120 )", "oklch(0.5 0.1 120DEG)",
	"oklch(50 % 0.1 120)", "oklch(.5 .1 .5)", "oklch(5. 1. 1.)", "oklch(+0.5 +0.1 -30)", "oklch(0.5 0.1 -30)", "oklch(0.5 0.1 725)",
	"oklch(1e-1 1e-1 1e2)", "oklch(1E-1 1E-1 1E2)", "oklch(0.5 -0.1 10)", "oklch(1.5 0.1 10)", "oklch(150% 0.1 10)", "oklch(0.5 0.1)",
	"oklch(0.5,0.1,10)", "oklch(0.5 0.1 10) ", "oklch(0.5 0.1 10deg )", "oklch(0.5 0.1 10 deg)", "oklch(0.5%  0.1 10)", "oklch(0.5 0.1% 10)",
	"oklch(100% 0.3 150)", "oklch(0% 0.3 150)", "oklch(0.5 0.1 1e400)", "oklch(1e400 0.1 1)", "oklch(0.5 1e400 1)",
	`oklch(${nbsp}0.5${nbsp}0.1${nbsp}10${nbsp})`, `oklch(${ls}0.5${ls}0.1${ls}10)`, `oklch(${bom}0.5 0.1 10)`, `oklch(0.5${ideo}0.1${ideo}10)`, `oklch(${nel}0.5 0.1 10)`, `oklch(0.5${vt}0.1 10)`,
	"okhsl(250 100% 50%)", "okhsl(250deg 1 0.5)", "okhsl(250 160% 55%)", "okhsl(250 50% 150%)", "okhsl(250 0.5 0.5%)", "OKHSL( 10  50%  50% )",
	"okhsl(-10 50% 50%)", "okhsl(370 50% 50%)", "okhsl(1e2 5e-1 5e-1)", "okhsl(0 0 0)", "okhsl(0 0 1)", "okhsl(0 1 1)", "okhsl(0 1 0)", "okhsl(180 100% 100%)",
	`okhsl(${nbsp}250 100% 50%)`, "okhsl(250 100%50%)", "okhsl(250 100 % 50%)", "okhsl(250% 100% 50%)",
	"", "red", "rgb(1 2 3)", "oklch()", "oklch(a b c)", "okhsl(250 100% 50%) extra", "okhsl(250,100%,50%)",
	"oklch(0.5 0.1 10)\n", "\noklch(0.5 0.1 10)", "#fff\n", "#fff\u2028",
];
const out = inputs.map((input) => {
	try {
		const color = tui.parseColor(input);
		const rgb = tui.colorToRgb(color);
		return { input, ok: true, kind: color.kind, rgb, hex: tui.colorToHex(color), fg256: tui.foregroundAnsi(color, "256color"), fgTrue: tui.foregroundAnsi(color, "truecolor"), oklch: tui.colorToOklch(color) };
	} catch (error) {
		return { input, ok: false, error: error.message };
	}
});
const numbers = [0, 1, 15, 16, 231, 232, 255, 256, -1, 1.5, NaN, Infinity].map((n) => {
	try {
		const color = tui.parseColor(n);
		return { input: String(n), ok: true, index: color.index, rgb: tui.colorToRgb(color), fg256: tui.foregroundAnsi(color, "256color"), fgTrue: tui.foregroundAnsi(color, "truecolor") };
	} catch (error) {
		return { input: String(n), ok: false, error: error.message };
	}
});
process.stdout.write(JSON.stringify({ strings: out, numbers }, null, 1) + "\n");
