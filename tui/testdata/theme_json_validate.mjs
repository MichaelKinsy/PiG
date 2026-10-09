// Validate theme documents with the installed Pi's validateThemeJson (theme-json.ts) and print each message.
import { readFileSync, readdirSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { validateThemeJson } = await import(pathToFileURL(root + "dist/modes/interactive/theme/theme-json.js"));
const dir = process.argv[3];
const out = [];
for (const name of readdirSync(dir).sort((a, b) => Number(a) - Number(b))) {
  const json = JSON.parse(readFileSync(`${dir}/${name}`, "utf8"));
  try { validateThemeJson(`doc-${name}`, json); out.push(""); } catch (e) { out.push(e.message); }
}
process.stdout.write(JSON.stringify(out), () => process.exit(0));
