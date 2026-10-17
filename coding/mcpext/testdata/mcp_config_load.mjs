// Runs the installed Pi's loadMcpConfig over {global, project, trusted} mcp.json contents (string or null) and prints the servers, autoEnableCodemode and errors.
import { mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, sep } from "node:path";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { loadMcpConfig } = await import(pathToFileURL(root + "dist/extensions/mcp/config.js"));
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const base = mkdtempSync(join(tmpdir(), "pi-mcp-"));
const results = JSON.parse(input).map(({ global, project, trusted }, index) => {
	const agentDir = join(base, `${index}`, "agent");
	const cwd = join(base, `${index}`, "work");
	mkdirSync(agentDir, { recursive: true });
	mkdirSync(join(cwd, ".pi"), { recursive: true });
	if (global !== null) writeFileSync(join(agentDir, "mcp.json"), global);
	if (project !== null) writeFileSync(join(cwd, ".pi", "mcp.json"), project);
	const loaded = loadMcpConfig({ agentDir, cwd, projectTrusted: trusted });
	const prefix = join(base, `${index}`) + sep;
	const rel = (p) => (p === undefined ? undefined : p.replace(prefix, ""));
	return JSON.stringify({
		servers: loaded.servers.map((s) => ({ name: s.name, scope: s.scope, source: rel(s.source), override: rel(s.override), config: s.config })),
		autoEnableCodemode: loaded.autoEnableCodemode ?? null,
		errors: loaded.errors.map((e) => e.split(prefix).join("")),
		projectConfig: rel(loaded.projectConfig) ?? null,
	});
});
rmSync(base, { recursive: true, force: true });
process.stdout.write(JSON.stringify(results));
