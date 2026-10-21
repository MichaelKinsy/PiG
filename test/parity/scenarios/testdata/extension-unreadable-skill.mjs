// Discovers one skill in a fresh directory and removes its file when the first input arrives, so the /skill: expansion
// that follows the input event cannot read it.
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

export default function (pi) {
	let file;
	pi.on("resources_discover", () => {
		const root = mkdtempSync(join(tmpdir(), "pig-parity-skill-"));
		mkdirSync(join(root, "boom"));
		file = join(root, "boom", "SKILL.md");
		writeFileSync(file, "---\nname: boom\ndescription: Unreadable once the prompt starts.\n---\nbody\n");
		return { skillPaths: [root] };
	});
	pi.on("input", () => {
		rmSync(file);
	});
}
