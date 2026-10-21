import { SAMPLES, THEMES, useTheme } from "../../../../../automation/gen/sdk-vector-lib/theme-state.mjs";

export const cases = THEMES.map(([name, mode]) => ({ name: `${name} ${mode}`, args: [name, mode] }));

export async function call(fn, themeName, colorMode) {
  const palette = await useTheme(this, themeName, colorMode);
  const theme = fn();
  const styles = {};
  for (const [key, style] of Object.entries(theme)) styles[key] = SAMPLES.map((text) => style(text));
  return { palette, keys: Object.keys(theme), styles };
}
