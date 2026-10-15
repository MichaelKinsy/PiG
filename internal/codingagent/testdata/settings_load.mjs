// Runs the installed Pi's SettingsManager.create over settings file contents ({global, project}: string or null) and prints the loaded layers and the load errors.
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { SettingsManager } = await import(pathToFileURL(root + "dist/core/settings-manager.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const base = mkdtempSync(join(tmpdir(), "pi-settings-"));
const results = JSON.parse(input).map(({ global, project }, index) => {
	const agentDir = join(base, `${index}`, "agent");
	const cwd = join(base, `${index}`, "work");
	mkdirSync(agentDir, { recursive: true });
	mkdirSync(join(cwd, ".pi"), { recursive: true });
	if (global !== null) writeFileSync(join(agentDir, "settings.json"), global);
	if (project !== null) writeFileSync(join(cwd, ".pi", "settings.json"), project);
	const manager = SettingsManager.create(cwd, agentDir);
	return {
		global: JSON.stringify(manager.getGlobalSettings()),
		project: JSON.stringify(manager.getProjectSettings()),
		merged: JSON.stringify(manager.getSettings()),
		errors: manager.drainErrors().map((e) => ({ scope: e.scope, message: e.error.message, path: e.path?.replace(base, "$BASE") })),
	};
});
process.stdout.write(JSON.stringify(results));
