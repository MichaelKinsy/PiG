// Load theme files with the installed Pi's loadThemeFromPath (theme.ts) in both colour modes: the error message, or every foreground and background escape and the appearance.
import { readFileSync, readdirSync, realpathSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (p) => import(pathToFileURL(root + p));
const { loadThemeFromPath, setThemeJsonValidator } = await load("dist/modes/interactive/theme/theme.js");
const { validateThemeJson } = await load("dist/modes/interactive/theme/theme-schema.js");
setThemeJsonValidator(validateThemeJson); // main.ts:69, 576 imports the schema validator from theme-schema.ts and installs it before any theme loads
const dir = process.argv[3];
const out = [];
for (const name of readdirSync(dir).sort((a, b) => parseInt(a) - parseInt(b))) {
  const entry = {};
  for (const mode of ["truecolor", "256color"]) {
    try {
      // join, not `${dir}/${name}`: the error message names the path, and Go's filepath.Join uses the host separator.
      const theme = loadThemeFromPath(join(dir, name), mode);
      const fg = {}, bg = {};
      for (const token of theme.fgAnsi.keys()) fg[token] = theme.getFgAnsi(token);
      for (const token of theme.bgAnsi.keys()) bg[token] = theme.getBgAnsi(token);
      entry[mode] = { name: theme.name, appearance: theme.appearance, fg, bg };
    } catch (e) {
      entry[mode] = { error: e.message };
    }
  }
  out.push(entry);
}
process.stdout.write(JSON.stringify(out), () => process.exit(0));
