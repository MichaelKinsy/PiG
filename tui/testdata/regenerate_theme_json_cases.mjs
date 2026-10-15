// Re-record theme_json_cases.json: each case's input is kept, each error is the installed Pi's validateThemeJson message.
// Usage: node regenerate_theme_json_cases.mjs <pi package version>
import { readFileSync, realpathSync, writeFileSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { validateThemeJson } = await import(pathToFileURL(root + "dist/modes/interactive/theme/theme-schema.js"));
const file = new URL("theme_json_cases.json", import.meta.url);
const cases = JSON.parse(readFileSync(file, "utf8"));
for (const c of cases) {
  try { validateThemeJson(c.label, JSON.parse(c.input)); c.error = null; } catch (e) { c.error = e.message; }
}
writeFileSync(file, JSON.stringify(cases, null, 1) + "\n");
