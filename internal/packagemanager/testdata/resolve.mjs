// Runs the installed Pi's DefaultPackageManager.resolve() over fixture trees (file lists with contents, written by the caller) with PI_OFFLINE=1
// and prints each resource type's list: path (relative to the case directory), enabled and metadata.
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { DefaultPackageManager } = await import(pathToFileURL(root + "dist/core/package-manager.js"));
const { SettingsManager } = await import(pathToFileURL(root + "dist/core/settings-manager.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const base = realpathSync(mkdtempSync(join(tmpdir(), "pi-resolve-")));
process.env.PI_OFFLINE = "1";
const results = [];
for (const [index, fixture] of JSON.parse(input).entries()) {
	const dir = join(base, `${index}`);
	for (const [file, content] of Object.entries(fixture.files)) {
		mkdirSync(dirname(join(dir, file)), { recursive: true });
		writeFileSync(join(dir, file), content.split("$D").join(dir));
	}
	process.env.HOME = join(dir, "home");
	const cwd = join(dir, "work", "proj");
	const agentDir = join(dir, "agent");
	mkdirSync(cwd, { recursive: true });
	mkdirSync(agentDir, { recursive: true });
	const rel = (p) => (typeof p === "string" ? p.split(dir).join("$D") : p);
	try {
		const settingsManager = SettingsManager.create(cwd, agentDir);
		const manager = new DefaultPackageManager({ cwd, agentDir, settingsManager, builtinExtensions: fixture.builtin });
		const resolved = await manager.resolve();
		const out = {};
		for (const type of ["extensions", "skills", "prompts", "themes"]) {
			out[type] = resolved[type].map((r) => ({ path: rel(r.path), enabled: r.enabled, source: rel(r.metadata.source), scope: r.metadata.scope, origin: r.metadata.origin, baseDir: rel(r.metadata.baseDir ?? ""), packageRoot: rel(r.metadata.packageRoot ?? "") }));
		}
		results.push(out);
	} catch (error) {
		results.push({ error: rel(error.message) });
	}
}
process.stdout.write(JSON.stringify(results));
