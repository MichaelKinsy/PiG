// Run the installed Pi's InteractiveMode.renderInitialMessages (interactive-mode.ts) over a recording `this`, never a translated oracle.
// The stub records what the method asks of its collaborators, in order: the entries it renders and with which options, the trust warning, and the status text.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "0";
const load = (path) => import(pathToFileURL(root + path));
const { initTheme } = await load("dist/modes/interactive/theme/theme.js");
initTheme("dark");
const { InteractiveMode } = await load("dist/modes/interactive/interactive-mode.js");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const probe of JSON.parse(input)) {
  const calls = [];
  const target = {
    sessionManager: {
      buildContextEntries: () => { calls.push({ call: "buildContextEntries" }); return probe.contextEntries; },
      getEntries: () => { calls.push({ call: "getEntries" }); return probe.allEntries; },
    },
    renderSessionEntries: (entries, options) => calls.push({ call: "renderSessionEntries", entries: entries.map((entry) => entry.id), options }),
    renderProjectTrustWarningIfNeeded: () => calls.push({ call: "renderProjectTrustWarningIfNeeded" }),
    showStatus: (message) => calls.push({ call: "showStatus", message }),
  };
  InteractiveMode.prototype.renderInitialMessages.call(target);
  results.push(calls);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
