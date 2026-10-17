// Runs the installed Pi's SessionManager.open over session files built from cases ({version, lines}, JSON on stdin) and prints, per case, either the thrown message or the entries, leaf id and branch ids it reports.
import { mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { SessionManager } = await import(pathToFileURL(root + "dist/core/session-manager.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const dir = mkdtempSync(join(tmpdir(), "pi-open-"));
const results = JSON.parse(input).map((lines, index) => {
	const file = join(dir, `${index}.jsonl`);
	writeFileSync(file, lines.join("\n") + "\n");
	try {
		const session = SessionManager.open(file, dir, "/w");
		const leaf = session.getLeafId();
		return { entries: session.getEntries().map((entry) => JSON.stringify(entry)), leaf: leaf === undefined ? "undefined" : leaf, branch: session.getBranch().map((entry) => JSON.stringify(entry)) };
	} catch (error) {
		return { throws: String(error.message) };
	}
});
process.stdout.write(JSON.stringify(results));
