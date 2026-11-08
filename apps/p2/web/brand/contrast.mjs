// WCAG 2.x contrast of every text/background pair the brand tokens put on screen: AA is 4.5:1 for body text, 3:1 for large
// text and UI. Prints a table; exits 1 when a pair fails its threshold.   node contrast.mjs [--markdown]
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const tokens = JSON.parse(readFileSync(join(dirname(fileURLToPath(import.meta.url)), "tokens.json"), "utf8"));
const channel = (value) => {
	const c = value / 255;
	return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
};
const luminance = (hex) => {
	const [r, g, b] = [1, 3, 5].map((at) => Number.parseInt(hex.slice(at, at + 2), 16));
	return 0.2126 * channel(r) + 0.7152 * channel(g) + 0.0722 * channel(b);
};
export const ratio = (a, b) => {
	const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
	return (hi + 0.05) / (lo + 0.05);
};
// [foreground, background, minimum, what]
const pairs = [
	["text", "bg", 4.5, "body text on the page"],
	["text", "soft", 4.5, "text on hover rows and the user's bubble"],
	["text", "code", 4.5, "code and inline code"],
	["muted", "bg", 4.5, "secondary text on the page"],
	["muted", "soft", 4.5, "secondary text on hover rows"],
	["muted", "code", 4.5, "secondary text in code blocks"],
	["faint", "bg", 3, "placeholders and tertiary labels (UI text)"],
	["faint", "code", 3, "comments in code blocks"],
	["accentText", "bg", 4.5, "links and accent text on the page"],
	["accentText", "soft", 4.5, "accent text on soft surfaces"],
	["accentText", "code", 4.5, "accent text on code"],
	["warning", "bg", 4.5, "warning text on the page"],
	["bad", "bg", 4.5, "error text on the page"],
	["addInk", "add", 4.5, "added lines in diffs"],
	["delInk", "del", 4.5, "removed lines in diffs"],
	["keyword", "code", 4.5, "code: keywords"],
	["string", "code", 4.5, "code: strings"],
	["number", "code", 4.5, "code: numbers"],
	["title", "code", 4.5, "code: function names"],
	["type", "code", 4.5, "code: types"],
	["attr", "code", 4.5, "code: attributes"],
	["line", "bg", 1.25, "separators (decorative; not required by WCAG)"],
	["scrollbar", "bg", 1.3, "scrollbar thumbs (decorative)"],
];
let failed = false;
const rows = ["| theme | foreground | background | ratio | needs | use |", "|---|---|---|---|---|---|"];
for (const theme of ["light", "dark"]) {
	const palette = tokens[theme];
	for (const [foreground, background, minimum, what] of pairs) {
		const value = ratio(palette[foreground], palette[background]);
		const ok = value >= minimum;
		failed ||= !ok;
		rows.push(`| ${theme} | ${foreground} ${palette[foreground]} | ${background} ${palette[background]} | ${value.toFixed(2)}${ok ? "" : " FAIL"} | ${minimum} | ${what} |`);
	}
}
if (process.argv[1] === fileURLToPath(import.meta.url)) {
	console.log(rows.join("\n"));
	process.exit(failed ? 1 : 0);
}
