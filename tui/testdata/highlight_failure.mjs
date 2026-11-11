// theme.ts highlightCode of the installed Pi under a theme that has no color for the syntaxKeyword token: cli-highlight's formatter calls theme.fg, which throws
// `Unknown theme color`, so the highlight fails and highlightCode returns the code in the mdCodeBlock color. The Markdown component renders each probe's source with
// that theme's getMarkdownTheme(), whose highlightCode is the same function. Code that needs no keyword color highlights as usual.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (p) => import(pathToFileURL(root + p));
const { Markdown, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { Theme, initTheme, getMarkdownTheme } = await load("dist/modes/interactive/theme/theme.js");
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
initTheme("dark");
const dark = JSON.parse(readFileSync(root + "dist/modes/interactive/theme/dark.json", "utf8"));
const resolve = (value) => (typeof value === "string" && value in dark.vars ? resolve(dark.vars[value]) : value);
const fg = {};
const bg = {};
for (const [token, value] of Object.entries(dark.colors)) {
  if (token === "syntaxKeyword") continue;
  (token.endsWith("Bg") ? bg : fg)[token] = resolve(value);
}
const broken = new Theme(fg, bg, "truecolor", { name: "dark" });
globalThis[Symbol.for("@earendil-works/pi-coding-agent:theme")] = broken;
globalThis[Symbol.for("@mariozechner/pi-coding-agent:theme")] = broken;
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((source) => {
  const markdown = getMarkdownTheme();
  return { rendered: new Markdown(source.text, 0, 0, markdown).render(40), highlight: markdown.highlightCode(source.code, source.lang) };
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
