// Run the installed Pi's getLanguageFromPath and highlightCode (theme.ts) over paths whose extension names a member of Object.prototype, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { getLanguageFromPath, highlightCode, initTheme } = await import(pathToFileURL(root + "dist/modes/interactive/theme/theme.js"));
initTheme("dark");
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const rows = JSON.parse(input).map((path) => {
  const value = getLanguageFromPath(path);
  let throws = "";
  try {
    highlightCode("const x = 1;", value);
  } catch (error) {
    throws = String(error);
  }
  return { language: typeof value === "string" ? value : "", inherited: value !== undefined && typeof value !== "string", throws };
});
process.stdout.write(JSON.stringify(rows), () => process.exit(0));
