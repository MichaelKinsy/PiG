import chalk from "./shims/chalk/source/index.js";
import { Theme } from "./shims/pi-dist/pi-coding-agent/modes/interactive/theme/theme.js";

const FAINT = "\x1b[2m";

// A well-formed pi-tui Color from its wire object, or undefined.
function wireColor(value) {
  if (value === null || typeof value !== "object") return undefined;
  const finite = (...keys) => keys.every((key) => typeof value[key] === "number" && Number.isFinite(value[key]));
  switch (value.kind) {
    case "indexed":
      return finite("index") && Number.isInteger(value.index) && value.index >= 0 && value.index <= 255 ? Object.freeze({ kind: "indexed", index: value.index }) : undefined;
    case "rgb":
      return finite("r", "g", "b") ? Object.freeze({ kind: "rgb", r: value.r, g: value.g, b: value.b }) : undefined;
    case "oklch":
      return finite("l", "c", "h") ? Object.freeze({ kind: "oklch", l: value.l, c: value.c, h: value.h }) : undefined;
  }
  return undefined;
}

// The palette's concrete colors by token, frozen like Pi's Theme.colors. A color of an unknown kind is not a color; it is dropped, as a token without an escape sequence is.
export function wireColors(colors) {
  const result = {};
  if (colors && typeof colors === "object") {
    for (const [token, value] of Object.entries(colors)) {
      const color = wireColor(value);
      if (color) result[token] = color;
    }
  }
  return Object.freeze(result);
}

// The host sends each foreground as Pi's getFgAnsi opening, which appends SGR 2 to a faint token's color (.upstream/v0.99.1/packages/coding-agent/src/modes/interactive/theme/theme.ts:399-402). A color opening never ends in SGR 2, so the suffix identifies the faint tokens that fg and style close with SGR 22 (theme.ts:344,363).
function splitFaint(foregrounds) {
  const fgAnsi = new Map();
  const dimTokens = new Set();
  for (const [token, ansi] of Object.entries(foregrounds)) {
    if (typeof ansi === "string" && ansi.endsWith(FAINT)) {
      fgAnsi.set(token, ansi.slice(0, -FAINT.length));
      dimTokens.add(token);
    } else fgAnsi.set(token, ansi);
  }
  return { fgAnsi, dimTokens };
}

// The host palette already contains resolved ANSI colors in the terminal's color mode. Rehydrate Pi's Theme data without selecting it or converting those colors a second time.
export function themeFromPalette(palette) {
  if (!palette) return undefined;
  // Pi's Theme styles use its shared Chalk capability, not the subprocess pipe's color detection.
  chalk.level = palette.modifiers === false ? 0 : palette.mode === "256color" ? 2 : 3;
  const { fgAnsi, dimTokens } = splitFaint(palette.foregrounds);
  const theme = Object.assign(Object.create(Theme.prototype), {
    name: palette.name,
    sourcePath: palette.sourcePath,
    sourceInfo: palette.sourceInfo,
    // .upstream/v0.99.1/packages/coding-agent/src/modes/interactive/theme/theme.ts:248-256,301-306 (fgAnsi, bgAnsi, concreteColors, dimTokens, ownAppearance) and 372-376 (tokenAnsi). The host resolves the concrete colors, the tokens with no color of their own and the appearance with the palette (theme.ts:306,312-336), so they are not rebuilt here: concreteColors and the default-token lists stay empty.
    fgAnsi,
    bgAnsi: new Map(Object.entries(palette.backgrounds)),
    concreteColors: {},
    defaultForegroundTokens: [],
    defaultBackgroundTokens: [],
    dimTokens,
    ownAppearance: palette.appearance === "light" || palette.appearance === "dark" ? palette.appearance : undefined,
    resolvedColors: undefined,
    mode: palette.mode,
  });
  // Theme.colors would mix a faint token toward the background a second time; the host's colors already are that.
  if (palette.colors) Object.defineProperty(theme, "colors", { value: wireColors(palette.colors), enumerable: true });
  return theme;
}
