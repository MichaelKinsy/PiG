// Reads [{content, prefix, paths: [[relPath, isDirectory], ...]}] on stdin; prints, per case, whether Pi's package manager ignores each path.
// prefixIgnorePattern and addIgnoreRules are copied from packages/coding-agent/src/core/package-manager.ts (v1.1.0); the matcher is the real `ignore` package.
import { createRequire } from "node:module";
import { realpathSync } from "node:fs";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url));
const ignore = createRequire(root + "/package.json")("ignore");

function prefixIgnorePattern(line, prefix) {
	const trimmed = line.trim();
	if (!trimmed) return null;
	if (trimmed.startsWith("#") && !trimmed.startsWith("\\#")) return null;
	let pattern = line;
	let negated = false;
	if (pattern.startsWith("!")) {
		negated = true;
		pattern = pattern.slice(1);
	} else if (pattern.startsWith("\\!")) {
		pattern = pattern.slice(1);
	}
	if (pattern.startsWith("/")) {
		pattern = pattern.slice(1);
	}
	const prefixed = prefix ? `${prefix}${pattern}` : pattern;
	return negated ? `!${prefixed}` : prefixed;
}

process.stdin.setEncoding("utf8");
let text = "";
for await (const chunk of process.stdin) text += chunk;
const results = JSON.parse(text).map(({ content, prefix, paths }) => {
	const ig = ignore();
	const patterns = content
		.split(/\r?\n/)
		.map((line) => prefixIgnorePattern(line, prefix))
		.filter((line) => Boolean(line));
	if (patterns.length > 0) ig.add(patterns);
	return paths.map(([rel, isDir]) => {
		try {
			return ig.ignores(isDir ? `${rel}/` : rel);
		} catch (error) {
			return `error: ${error.message}`;
		}
	});
});
process.stdout.write(JSON.stringify(results));
