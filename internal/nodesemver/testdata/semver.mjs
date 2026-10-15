// Reads {versions, ranges} on stdin and prints validRange(range), satisfies(version, range) for every pair, maxSatisfying(versions, range) and parse(version).version / compare of neighbouring versions.
import { createRequire } from "node:module";
import { realpathSync } from "node:fs";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url));
const semver = createRequire(root + "/package.json")("semver");
process.stdin.setEncoding("utf8");
let text = "";
for await (const chunk of process.stdin) text += chunk;
const { versions, ranges } = JSON.parse(text);
const guard = (f) => {
	try {
		return f();
	} catch (error) {
		return `throws ${error.constructor.name}`;
	}
};
process.stdout.write(
	JSON.stringify({
		parse: versions.map((v) => guard(() => semver.parse(v)?.version ?? null)),
		compare: versions.map((v, i) => guard(() => { const a = semver.parse(v), b = semver.parse(versions[(i + 1) % versions.length]); return a && b ? a.compare(b) : null; })),
		validRange: ranges.map((r) => guard(() => semver.validRange(r))),
		satisfies: ranges.map((r) => versions.map((v) => guard(() => semver.satisfies(v, r)))),
		maxSatisfying: ranges.map((r) => guard(() => semver.maxSatisfying(versions, r))),
	}),
);
