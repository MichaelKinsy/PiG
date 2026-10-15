// Drive the installed Pi renderToolPath (core/tools/render-utils.ts) directly, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (path) => import(pathToFileURL(root + path));
const { setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { initTheme, theme } = await load("dist/modes/interactive/theme/theme.js");
const { renderToolPath } = await load("dist/core/tools/render-utils.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  process.env.HOME = probe.home;
  setCapabilities({ images: null, trueColor: true, hyperlinks: probe.hyperlinks });
  initTheme(probe.theme);
  try {
    return { out: renderToolPath(probe.path, theme, probe.cwd, probe.emptyFallback === null ? undefined : { emptyFallback: probe.emptyFallback }) };
  } catch (e) {
    return { error: String(e && e.message).slice(0, 80) };
  }
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
