// Drive the installed Pi's SessionManager.appendMessage with each member of its parameter union, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { SessionManager } = await import(pathToFileURL(root + "dist/core/session-manager.js"));
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const results = [];
for (const messages of JSON.parse(input)) {
	const manager = SessionManager.inMemory("/tmp/pi-append-message-oracle");
	const ids = messages.map((message) => manager.appendMessage(message));
	const entries = manager.getEntries();
	results.push({
		entries: entries.map((entry) => ({ type: entry.type, message: entry.message, parentIsPrevious: true })),
		parents: entries.map((entry, i) => entry.parentId === (i === 0 ? null : ids[i - 1])),
		ids: ids.map((id, i) => id === entries[i].id),
		contextRoles: manager.buildSessionContext().messages.map((message) => message.role),
	});
}
process.stdout.write(JSON.stringify(results));
