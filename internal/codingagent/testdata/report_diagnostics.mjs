// Style diagnostics with the chalk 6 that Pi 1.1.0 installs, as main.ts reportDiagnostics does, at color level 1.
import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
const root = new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url);
if (JSON.parse(readFileSync(new URL("package.json", root), "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const chalkDir = new URL("node_modules/chalk/", root);
const { Chalk } = await import(pathToFileURL(createRequire(chalkDir).resolve("./source/index.js")).href);
const chalk = new Chalk({ level: 1 });
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
// The body of main.ts reportDiagnostics, which is not exported, with chalk at level 1.
const out = JSON.parse(input).map((diagnostic) => {
	const color = diagnostic.type === "error" ? chalk.red : diagnostic.type === "warning" ? chalk.yellow : chalk.dim;
	const prefix = diagnostic.type === "error" ? "Error: " : diagnostic.type === "warning" ? "Warning: " : "";
	return color(`${prefix}${diagnostic.message}`);
});
process.stdout.write(JSON.stringify(out));
