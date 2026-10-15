// Runs the installed Pi's ProjectTrustStore over a trust.json and scripted get/set/setMany calls, reporting each call's result or error message and the final file bytes.
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { ProjectTrustStore } = await import(pathToFileURL(root + "dist/core/trust-manager.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const base = realpathSync(mkdtempSync(join(tmpdir(), "pi-trust-")));
const results = JSON.parse(input).map((script, index) => {
	const dir = join(base, `${index}`);
	const escapedDir = dir.split("\\").join("\\\\"); // the path as it reads inside trust.json text on Windows
	const unbase = (s) => s.split(escapedDir).join("$BASE").split(dir).join("$BASE");
	const agentDir = join(dir, "agent");
	mkdirSync(agentDir, { recursive: true });
	for (const sub of script.dirs) mkdirSync(join(dir, sub), { recursive: true });
	if (script.initial !== null) writeFileSync(join(agentDir, "trust.json"), script.initial.split("$BASE").join(escapedDir));
	const store = new ProjectTrustStore(agentDir);
	const outcomes = script.ops.map((op) => {
		try {
			const abs = (p) => dir + "/" + p;
			if (op.op === "get") return JSON.stringify(store.get(abs(op.path)));
			if (op.op === "entry") {
				const entry = store.getEntry(abs(op.path));
				return JSON.stringify(entry === null ? null : { path: unbase(entry.path), decision: entry.decision });
			}
			if (op.op === "set") store.set(abs(op.path), op.decision);
			else store.setMany(op.updates.map((u) => ({ path: abs(u.path), decision: u.decision })));
			return "ok";
		} catch (error) {
			return `error ${unbase(error.message)}`;
		}
	});
	let text = null;
	try {
		text = unbase(readFileSync(join(agentDir, "trust.json"), "utf8"));
	} catch {}
	return { outcomes, text };
});
process.stdout.write(JSON.stringify(results));
