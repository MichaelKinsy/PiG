// Run the installed pi-tui wordWrapLine over seeded [line, width] pairs; a case Pi cannot wrap reports the thrown error's name.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { wordWrapLine } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/components/editor.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const results = JSON.parse(input).map(([line, width]) => {
  try {
    return wordWrapLine(line, width);
  } catch (error) {
    return error.name;
  }
});
process.stdout.write(JSON.stringify(results));
