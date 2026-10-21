import { THEMES, useTheme, withPlatform } from "../../../../../automation/gen/sdk-vector-lib/theme-state.mjs";

const KEYS = ["", "enter", "alt+enter", "ctrl+alt+x", "ALT+x", "Alt+Left", "alt+/ctrl+b", "shift+tab/alt+x/ctrl+c", "alt", "a+b+c", "/", "+", "alt+", "x/", "é+alt+ü", "ß", "øption", "alt+ß"];

export const cases = [];
for (const [theme, mode] of THEMES.slice(0, 2)) {
  for (const platform of ["linux", "darwin"]) {
    for (const key of KEYS) cases.push({ name: `${theme} ${mode} ${platform} ${JSON.stringify(key)}`, args: [theme, mode, platform, key, "desc"] });
  }
}

export async function call(fn, themeName, colorMode, platform, key, description) {
  const palette = await useTheme(this, themeName, colorMode);
  return { palette, platform, text: withPlatform(platform, () => fn(key, description)) };
}
