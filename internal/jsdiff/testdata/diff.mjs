// Reads [[old, new], ...] on stdin and prints diffWords and diffLines changes (value, count, added, removed) from the jsdiff Pi depends on.
import { createRequire } from "node:module";
import { realpathSync } from "node:fs";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url));
const { diffWords, diffLines } = createRequire(root + "/package.json")("diff");
process.stdin.setEncoding("utf8");
let text = "";
for await (const chunk of process.stdin) text += chunk;
const shape = (changes) => changes.map((c) => ({ value: c.value, count: c.count, added: !!c.added, removed: !!c.removed }));
process.stdout.write(
	JSON.stringify(
		JSON.parse(text).map(([a, b]) => ({ words: shape(diffWords(a, b)), lines: shape(diffLines(a, b)) })),
	),
);
