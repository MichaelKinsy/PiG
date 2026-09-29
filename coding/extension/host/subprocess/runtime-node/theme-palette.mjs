import chalk from "./shims/chalk/source/index.js";
import { Theme } from "./shims/pi-dist/pi-coding-agent/modes/interactive/theme/theme.js";

// The host palette already contains resolved ANSI colors in the terminal's color mode. Rehydrate Pi's Theme data without selecting it or converting those colors a second time.
export function themeFromPalette(palette) {
  if (!palette) return undefined;
  // Pi's Theme styles use its shared Chalk capability, not the subprocess pipe's color detection.
  chalk.level = palette.modifiers === false ? 0 : palette.mode === "256color" ? 2 : 3;
  return Object.assign(Object.create(Theme.prototype), {
    name: palette.name,
    sourcePath: palette.sourcePath,
    sourceInfo: palette.sourceInfo,
    fgColors: new Map(Object.entries(palette.foregrounds)),
    bgColors: new Map(Object.entries(palette.backgrounds)),
    mode: palette.mode,
  });
}
