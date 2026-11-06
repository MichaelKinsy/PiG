// The installed Pi's Markdown with theme.ts getMarkdownTheme() (highlight.js code blocks) over code holding lone UTF-16 surrogates. Probes and rows travel as
// JSON, where a lone surrogate is the escape JSON.stringify writes.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (p) => import(pathToFileURL(root + p));
const { Markdown, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { initTheme, getMarkdownTheme } = await load("dist/modes/interactive/theme/theme.js");
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  initTheme("dark");
  return new Markdown(probe.text, probe.paddingX, 0, getMarkdownTheme()).render(probe.width);
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
