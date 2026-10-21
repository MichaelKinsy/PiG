// Generates the pi-coding-agent theme and keybinding-hint vectors every extension SDK must reproduce.
//
// The expected values come from the pinned Pi package's own functions (keyHint, keyText, rawKeyHint, getMarkdownTheme, getSelectListTheme,
// getSettingsListTheme, getEditorTheme), run against a built-in theme in each color mode and a keybinding table. The palette the SDKs
// are given is the one the host publishes (`ExtensionThemePalette`): each token's opening escape sequence as Pi's Theme resolved it.
//
// Usage: node gen-coding-agent.mjs <pi-coding-agent package dir (with its node_modules)> <out dir>
//   e.g. (from test/parity/sdk-bulk) node gen-coding-agent.mjs <node_modules>/@earendil-works/pi-coding-agent .
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

process.env.FORCE_COLOR = "3"; // chalk (theme.bold and friends) must draw before it is imported
const [pkgDir, outDir] = process.argv.slice(2);
if (!pkgDir || !outDir) throw new Error("usage: gen-coding-agent.mjs <pi-coding-agent dir> <out dir>");
const load = (rel) => import(pathToFileURL(path.join(path.resolve(pkgDir), "dist", rel)).href);
const tuiKeybindings = await import(pathToFileURL(path.join(path.resolve(pkgDir), "node_modules/@earendil-works/pi-tui/dist/keybindings.js")).href);
const hints = await load("modes/interactive/components/keybinding-hints.js");
const themeModule = await load("modes/interactive/theme/theme.js");
const { THEME_TOKENS } = await load("modes/interactive/theme/theme-tokens.js");
const piVersion = JSON.parse(fs.readFileSync(path.join(path.resolve(pkgDir), "package.json"), "utf8")).version;

const platformDesc = Object.getOwnPropertyDescriptor(process, "platform");
const withPlatform = (platform, fn) => {
	Object.defineProperty(process, "platform", { value: platform });
	try {
		return fn();
	} finally {
		Object.defineProperty(process, "platform", platformDesc);
	}
};

const defs = {
	...tuiKeybindings.TUI_KEYBINDINGS,
	"app.interrupt": { defaultKeys: "escape", description: "Interrupt" },
	"app.test.multi": { defaultKeys: ["alt+x", "ctrl+shift+f5", "alt+/"], description: "Multi" },
	"app.test.none": { defaultKeys: [], description: "None" },
	"app.test.slash": { defaultKeys: ["ctrl+/", "super+k"], description: "Slash" },
	"app.test.alt": { defaultKeys: "alt+alt", description: "Alt" },
};
const users = [{}, { "app.interrupt": ["ctrl+c", "alt+q"], "tui.select.confirm": [] }];
const keyTexts = ["", "enter", "alt+enter", "ctrl+alt+x", "ALT+x", "Alt+Left", "alt+/ctrl+b", "shift+tab/alt+x/ctrl+c", "alt", "a+b+c", "/", "+", "alt+", "x/", "é+alt+ü", "ß", "øption", "alt+ß"];
const samples = ["", "x", "to do it", "a\nb", "漢字", "\x1b[1m", "  "];

const out = { pi: piVersion, node: process.version, hints: [], themes: [] };

for (const platform of ["darwin", "linux", "win32"]) {
	for (const key of keyTexts) {
		for (const capitalize of [false, true]) {
			out.hints.push({ op: "formatKeyText", platform, key, capitalize, result: withPlatform(platform, () => hints.formatKeyText(key, { capitalize })) });
		}
	}
}

for (const [name, colorEnv] of [["dark", "truecolor"], ["light", ""]]) {
	if (colorEnv) process.env.COLORTERM = colorEnv;
	else delete process.env.COLORTERM;
	themeModule.initTheme(name);
	const theme = themeModule.theme;
	const foregrounds = {};
	const backgrounds = {};
	for (const [token, descriptor] of Object.entries(THEME_TOKENS)) {
		if (descriptor.slot === "background") backgrounds[token] = theme.getBgAnsi(token);
		else foregrounds[token] = theme.getFgAnsi(token);
	}
	const palette = { name, foregrounds, backgrounds, modifiers: true, mode: theme.getColorMode() };
	const entry = { name, palette, keyHints: [], markdown: {}, selectList: {}, settingsList: {}, editor: {} };
	for (const platform of ["darwin", "linux"]) {
		for (const user of users) {
			tuiKeybindings.setKeybindings(new tuiKeybindings.KeybindingsManager(defs, user));
			const row = { platform, user, ids: {}, raw: {} };
			for (const id of [...Object.keys(defs), "no.such.id"]) {
				row.ids[id] = withPlatform(platform, () => ({ keyText: hints.keyText(id), keyDisplayText: hints.keyDisplayText(id), keyHint: hints.keyHint(id, "to go") }));
			}
			for (const key of keyTexts) row.raw[key] = withPlatform(platform, () => hints.rawKeyHint(key, "desc"));
			entry.keyHints.push(row);
		}
	}
	const markdown = themeModule.getMarkdownTheme();
	for (const [fn, f] of Object.entries(markdown)) {
		if (fn === "highlightCode") continue;
		entry.markdown[fn] = samples.map((s) => f(s));
	}
	const select = themeModule.getSelectListTheme();
	for (const [fn, f] of Object.entries(select)) entry.selectList[fn] = samples.map((s) => f(s));
	const settings = themeModule.getSettingsListTheme();
	for (const [fn, f] of Object.entries(settings)) {
		if (typeof f === "string") entry.settingsList[fn] = f;
		else if (fn === "label" || fn === "value") entry.settingsList[fn] = samples.map((s) => [f(s, false), f(s, true)]);
		else entry.settingsList[fn] = samples.map((s) => f(s));
	}
	const editor = themeModule.getEditorTheme();
	entry.editor.borderColor = samples.map((s) => editor.borderColor(s));
	entry.editor.selectList = Object.fromEntries(Object.entries(editor.selectList).map(([fn, f]) => [fn, samples.map((s) => f(s))]));
	// The faint-token and unknown-token behavior of keyHint's theme: a missing token throws.
	entry.markdownKeys = Object.keys(markdown).filter((k) => k !== "highlightCode");
	out.themes.push(entry);
}
out.defs = defs;
fs.writeFileSync(path.join(outDir, "pi-coding-agent-helpers.json"), JSON.stringify(out) + "\n");
