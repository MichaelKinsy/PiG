// Drive the installed Pi's redactUrl and redactJsonValue over scripted inputs, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { redactUrl, redactJsonValue } = await import(pathToFileURL(root + "dist/core/bug-report.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const { urls, values } = JSON.parse(input);
process.stdout.write(JSON.stringify({ urls: urls.map((url) => redactUrl(url)), values: values.map((text) => redactJsonValue(JSON.parse(text))) }));
