import { useKeybindings, withPlatform } from "../../../../../automation/gen/sdk-vector-lib/theme-state.mjs";

const IDS = ["tui.select.cancel", "tui.select.confirm", "tui.editor.cursorLeft", "app.interrupt", "app.test.multi", "app.test.none", "app.test.slash", "app.test.alt", "app.test.unicode", "no.such.id"];

export const cases = [];
for (const platform of ["linux", "darwin", "win32"]) {
  for (const user of ["default", "overridden"]) {
    for (const id of IDS) cases.push({ name: `${platform} ${user} ${id}`, args: [platform, user, id] });
  }
}

export async function call(fn, platform, user, id) {
  const { defs, user: bindings } = await useKeybindings(this, user);
  return { platform, defs, user: bindings, text: withPlatform(platform, () => fn(id)) };
}
