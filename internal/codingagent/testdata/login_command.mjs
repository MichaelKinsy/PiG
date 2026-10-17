// Run the installed Pi's handleLoginCommand and findLoginProviderOptions (interactive-mode.ts) over a recording `this`, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "0";
const { InteractiveMode } = await import(pathToFileURL(root + "dist/modes/interactive/interactive-mode.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const { providers, ref } of JSON.parse(input)) {
  const record = [];
  const self = {
    findLoginProviderOptions: InteractiveMode.prototype.findLoginProviderOptions,
    getLoginProviderOptions: () => providers,
    startProviderLogin: async (provider) => { record.push(["start", provider.id, provider.authType]); },
    showLoginAuthTypeSelector: (options) => { record.push(["authType", options ? options.map((p) => `${p.id}/${p.authType}`) : null]); },
    showLoginProviderSelector: (authType, search) => { record.push(["providers", authType ?? null, search ?? null]); },
  };
  await InteractiveMode.prototype.handleLoginCommand.call(self, ref === "" ? undefined : ref);
  results.push(record);
}
process.stdout.write(JSON.stringify(results), () => process.exit(0));
