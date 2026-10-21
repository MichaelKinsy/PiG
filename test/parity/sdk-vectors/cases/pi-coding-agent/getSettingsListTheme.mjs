import { SAMPLES, THEMES, useTheme } from "../../../../../automation/gen/sdk-vector-lib/theme-state.mjs";

export const cases = THEMES.map(([name, mode]) => ({ name: `${name} ${mode}`, args: [name, mode] }));

// cursor is a string drawn when the theme is built; label and value take the selection; the rest are styles.
export async function call(fn, themeName, colorMode) {
  const palette = await useTheme(this, themeName, colorMode);
  const theme = fn();
  const styles = {};
  for (const [key, style] of Object.entries(theme)) {
    if (typeof style === "string") styles[key] = style;
    else if (key === "label" || key === "value") styles[key] = SAMPLES.map((text) => [style(text, false), style(text, true)]);
    else styles[key] = SAMPLES.map((text) => style(text));
  }
  return { palette, keys: Object.keys(theme), styles };
}
