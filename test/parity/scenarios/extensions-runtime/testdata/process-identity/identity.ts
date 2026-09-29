// Probes the process identity an extension sees: the title and markers Pi's
// setupCli sets, and process.argv[1] as the harness entry that extensions
// re-launch (@henryqw/pi-subagent, pi-subagents). The results are the
// description of the "process-identity" command.
import { spawnSync } from "node:child_process";
import * as fs from "node:fs";
import * as path from "node:path";

function harnessPackage(entry: string): { name?: string; binIsEntry?: boolean } {
	let dir = path.dirname(fs.realpathSync(entry));
	while (dir !== path.dirname(dir)) {
		const manifest = path.join(dir, "package.json");
		if (fs.existsSync(manifest)) {
			const pkg = JSON.parse(fs.readFileSync(manifest, "utf8"));
			if (pkg.name === "@earendil-works/pi-coding-agent") {
				const bin = typeof pkg.bin === "string" ? pkg.bin : pkg.bin?.pi;
				return { name: pkg.name, binIsEntry: path.resolve(dir, bin) === fs.realpathSync(entry) };
			}
		}
		dir = path.dirname(dir);
	}
	return {};
}

export default async function (pi: any) {
	const entry = process.argv[1];
	let warned = false;
	process.on("warning", () => {
		warned = true;
	});
	process.emitWarning("process-identity probe");
	await new Promise((resolve) => setImmediate(resolve));
	const relaunch = spawnSync(process.execPath, [entry, "--version"], { encoding: "utf8" });
	const identity = {
		title: process.title,
		piCodingAgent: process.env.PI_CODING_AGENT,
		aiAgent: process.env.AI_AGENT,
		warned,
		entryIsScript: /\.[cm]?js$/.test(fs.realpathSync(entry)),
		harness: harnessPackage(entry),
		relaunch: { status: relaunch.status, stdout: relaunch.stdout.trim() },
	};
	pi.registerCommand("process-identity", {
		description: JSON.stringify(identity),
		handler: async () => {},
	});
}
