package subprocess

import "testing"

// ctx.ui.theme's appearance, colors and style in the Node runtime (.upstream/v0.99.2/packages/coding-agent/src/modes/interactive/theme/theme.ts:311-367). The host resolves the appearance and the concrete colors with the palette (the terminal's reported colors are the host's), so the runtime answers them from the palette; style is Pi's own over the palette's escape sequences. The vectors are the sequences the host's tui.ForegroundAnsi and BackgroundAnsi produce for each color in each mode; test/extension-conformance/theme_test.go compares every SDK with the host for the same colors.
func TestNodeThemeAppearanceColorsAndStyle(t *testing.T) {
	nodeModelTypesScript(t, `
const E = "\x1b[";
const palette = (mode, extra = {}) => ({
  name: "dark", appearance: "light",
  foregrounds: { success: E + "38;2;1;2;3m", dimmed: E + "38;2;9;9;9m" + E + "2m", accent: E + "38;5;4m" },
  backgrounds: { toolSuccessBg: E + "48;2;4;5;6m", userMessageBg: E + "49m" },
  colors: { success: { kind: "rgb", r: 1, g: 2, b: 3 }, accent: { kind: "oklch", l: 0.62, c: 0.1, h: 200 }, toolSuccessBg: { kind: "indexed", index: 5 } },
  modifiers: true, mode, ...extra,
});
const runtime = new Runtime("/ext/theme.mjs");
const theme = runtime.ctx.ui.theme;

// Before any palette there is no appearance and no color.
assert.equal(theme.appearance, undefined);
assert.deepEqual(theme.colors, {});

theme.setPalette(palette("truecolor"));
assert.equal(theme.appearance, "light");
assert.deepEqual(theme.colors, palette("truecolor").colors);
assert.ok(Object.isFrozen(theme.colors), "colors is readonly");
// A palette without them leaves them undefined; a later palette replaces them whole.
theme.setPalette({ ...palette("truecolor"), appearance: undefined, colors: undefined });
assert.equal(theme.appearance, undefined);
assert.deepEqual(theme.colors, {});
theme.setPalette(palette("truecolor"));

const cases = [
  [{ fg: "success", bg: "toolSuccessBg", bold: true }, E + "38;2;1;2;3m" + E + "48;2;4;5;6m" + E + "1mx" + E + "22m" + E + "49m" + E + "39m"],
  [{}, "x"],
  [{ bold: true, dim: true, italic: true, underline: true, inverse: true, strikethrough: true }, E + "1m" + E + "2m" + E + "3m" + E + "4m" + E + "7m" + E + "9mx" + E + "29m" + E + "27m" + E + "24m" + E + "23m" + E + "22m"],
  [{ fg: "dimmed" }, E + "38;2;9;9;9m" + E + "2mx" + E + "22m" + E + "39m"],
  [{ fg: { kind: "rgb", r: 10, g: 20, b: 30 } }, E + "38;2;10;20;30mx" + E + "39m"],
  [{ fg: { kind: "rgb", r: 10.5, g: 20.4, b: 29.6 } }, E + "38;2;11;20;30mx" + E + "39m"],
  [{ fg: { kind: "oklch", l: 0.62, c: 0.1, h: 200 } }, E + "38;2;28;152;158mx" + E + "39m"],
  [{ bg: { kind: "oklch", l: 1, c: 0.3, h: 150 } }, E + "48;2;255;255;255mx" + E + "49m"],
  [{ fg: { kind: "indexed", index: 5 } }, E + "38;5;5mx" + E + "39m"],
];
for (const [options, want] of cases) assert.equal(theme.style("x", options), want, JSON.stringify(options));

theme.setPalette(palette("256color"));
for (const [color, want] of [
  [{ kind: "rgb", r: 10, g: 20, b: 30 }, E + "38;5;16mx" + E + "39m"],
  [{ kind: "rgb", r: 128, g: 128, b: 130 }, E + "38;5;244mx" + E + "39m"],
  [{ kind: "rgb", r: 250, g: 100, b: 50 }, E + "38;5;203mx" + E + "39m"],
  [{ kind: "oklch", l: 0.62, c: 0.1, h: 200 }, E + "38;5;31mx" + E + "39m"],
  [{ kind: "indexed", index: 5 }, E + "38;5;5mx" + E + "39m"],
]) assert.equal(theme.style("x", { fg: color }), want, JSON.stringify(color));

// theme-style.test.ts:43-48: an unknown token and a background token in the foreground slot.
assert.throws(() => theme.style("x", { fg: "notAToken" }), { message: "Unknown theme color: notAToken" });
assert.throws(() => theme.style("x", { fg: "toolSuccessBg" }), { message: "Unknown theme color: toolSuccessBg" });
assert.throws(() => theme.style("x", { bg: "success" }), { message: "Unknown theme color: success" });

// A later palette changes what the held theme answers.
theme.setPalette({ ...palette("truecolor"), appearance: "dark", foregrounds: { success: E + "38;2;7;8;9m" } });
assert.equal(theme.appearance, "dark");
assert.equal(theme.style("x", { fg: "success" }), E + "38;2;7;8;9mx" + E + "39m");

// Modifiers follow the host's styles as bold and the other chalk styles do; style does not (theme.ts:342 draws SGR directly).
theme.setPalette(palette("truecolor", { modifiers: false }));
assert.equal(theme.bold("x"), "x");
assert.equal(theme.style("x", { bold: true }), E + "1mx" + E + "22m");
`)
}
