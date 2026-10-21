import { THEMES, useKeybindings, useTheme, withPlatform } from "../../../../../automation/gen/sdk-vector-lib/theme-state.mjs";

const IDS = ["tui.select.cancel", "tui.select.confirm", "app.interrupt", "app.test.multi", "app.test.none", "app.test.alt", "app.test.unicode", "no.such.id"];

export const cases = [];
for (const [theme, mode] of THEMES.slice(0, 2)) {
  for (const platform of ["linux", "darwin"]) {
    for (const user of ["default", "overridden"]) {
      for (const id of IDS) cases.push({ name: `${theme} ${mode} ${platform} ${user} ${id}`, args: [theme, mode, platform, user, id, "to go"] });
    }
  }
}

export async function call(fn, themeName, colorMode, platform, user, id, description) {
  const palette = await useTheme(this, themeName, colorMode);
  const { defs, user: bindings } = await useKeybindings(this, user);
  return { palette, platform, defs, user: bindings, text: withPlatform(platform, () => fn(id, description)) };
}
