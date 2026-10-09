// Load models.json documents through the installed Pi's ModelConfig and print each load error.
import { readFileSync, readdirSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { ModelConfig } = await import(pathToFileURL(root + "dist/core/model-config.js"));
const dir = process.argv[3];
const names = readdirSync(dir).sort((a, b) => Number(a) - Number(b));
const out = [];
for (const name of names) {
  const config = await ModelConfig.load(`${dir}/${name}/models.json`);
  out.push(config.getError() ?? "");
}
process.stdout.write(JSON.stringify(out), () => process.exit(0));
