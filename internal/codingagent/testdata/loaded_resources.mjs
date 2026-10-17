// Drive the installed Pi's InteractiveMode resource-listing helpers (private methods, reached through the prototype).
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (p) => import(pathToFileURL(root + p));
const { setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
// The theme picks truecolor or 256 colors from the host TERM and COLORTERM; fix it so the probe output does not depend on them.
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
const { InteractiveMode } = await load("dist/modes/interactive/interactive-mode.js");
initTheme("dark");
const self = Object.create(InteractiveMode.prototype);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map((probe) => {
  const infos = new Map(Object.entries(probe.infos ?? {}));
  switch (probe.kind) {
    case "diagnostics":
      return self.formatDiagnostics(probe.diagnostics, infos);
    case "shortPath":
      return self.getShortPath(probe.path, probe.info ?? undefined);
    case "scopeGroups": {
      const groups = self.buildScopeGroups(probe.items);
      return self.formatScopeGroups(groups, {
        formatPath: (item) => self.formatDisplayPath(item.path),
        formatPackagePath: (item) => self.getShortPath(item.path, item.sourceInfo),
      });
    }
  }
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
