// Compares two generated catalogs file by file: the provider data directory and the public JSON tree. The data manifest's
// `generatedAt` is the one member that is allowed to differ (it is the generation time); every other byte must match.
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";

function files(root) {
	const out = [];
	const walk = (dir) => {
		for (const entry of readdirSync(dir).sort()) {
			const path = join(dir, entry);
			if (statSync(path).isDirectory()) walk(path);
			else out.push(relative(root, path));
		}
	};
	walk(root);
	return out;
}

function normalized(path, content) {
	if (!path.endsWith(".manifest.json")) return content;
	const manifest = JSON.parse(content);
	manifest.generatedAt = "<generatedAt>";
	return JSON.stringify(manifest);
}

const [left, right] = process.argv.slice(2);
let problems = 0;
let compared = 0;
for (const root of [left, right]) if (!existsSync(root)) throw new Error(`missing ${root}`);
const names = new Set([...files(left), ...files(right)]);
for (const name of [...names].sort()) {
	const a = existsSync(join(left, name)) ? readFileSync(join(left, name), "utf8") : undefined;
	const b = existsSync(join(right, name)) ? readFileSync(join(right, name), "utf8") : undefined;
	if (a === undefined || b === undefined) {
		console.log(`only in ${a === undefined ? right : left}: ${name}`);
		problems++;
		continue;
	}
	compared++;
	if (normalized(name, a) !== normalized(name, b)) {
		console.log(`DIFFERS: ${name}`);
		problems++;
	}
}
console.log(`${compared} files compared, ${problems} difference${problems === 1 ? "" : "s"}`);
process.exitCode = problems === 0 ? 0 : 1;
