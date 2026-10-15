// Runs the installed Pi's loadEntriesFromFile over session file contents (base64 on stdin) and prints, per file, the loaded entries as JSON and the file's bytes afterwards.
import { mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { loadEntriesFromFile } = await import(pathToFileURL(root + "dist/core/session-manager.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const dir = mkdtempSync(join(tmpdir(), "pi-load-"));
const results = JSON.parse(input).map((encoded, index) => {
	const file = join(dir, `${index}.jsonl`);
	writeFileSync(file, Buffer.from(encoded, "base64"));
	let entries;
	try {
		entries = JSON.stringify(loadEntriesFromFile(file));
	} catch (error) {
		entries = `throws ${error.constructor.name}`;
	}
	return { entries, after: readFileSync(file).toString("base64") };
});
process.stdout.write(JSON.stringify(results));
