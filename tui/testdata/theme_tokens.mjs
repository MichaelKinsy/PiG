// Every foreground and background token of the installed Pi's built-in themes in both color modes.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (p) => import(pathToFileURL(root + p));
const { setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { getThemeByName } = await load("dist/modes/interactive/theme/theme.js");
const out = {};
for (const name of ["dark", "light"]) {
  for (const mode of ["truecolor", "256color"]) {
    setCapabilities({ images: null, trueColor: mode === "truecolor", hyperlinks: false });
    const theme = getThemeByName(name);
    const entry = { mode: theme.getColorMode(), fg: {}, bg: {}, fgAnsi: {}, bgAnsi: {} };
    for (const token of theme.fgAnsi.keys()) {
      entry.fg[token] = theme.fg(token, "x");
      entry.fgAnsi[token] = theme.getFgAnsi(token);
    }
    for (const token of theme.bgAnsi.keys()) {
      entry.bg[token] = theme.bg(token, "x");
      entry.bgAnsi[token] = theme.getBgAnsi(token);
    }
    entry.styles = [theme.bold("x"), theme.italic("x"), theme.underline("x"), theme.inverse("x"), theme.strikethrough("x")];
    entry.thinking = Object.fromEntries(["off", "minimal", "low", "medium", "high", "xhigh", "max", "bogus"].map((l) => [l, theme.getThinkingBorderColor(l)("x")]));
    entry.bash = theme.getBashModeBorderColor()("x");
    out[`${name}/${mode}`] = entry;
  }
}
process.stdout.write(JSON.stringify(out), () => process.exit(0));
