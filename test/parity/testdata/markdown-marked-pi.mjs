// Oracle for tui/testdata/markdown-marked-corpus.json: renders every case with the pinned pi-tui Markdown component (marked 18.0.11) and the Chalk level-3 test theme of packages/tui/test/test-themes.ts, printing one {name, lines} JSON record per case.
// Regenerate the golden after changing the corpus:
//   node test/parity/testdata/markdown-marked-pi.mjs > tui/testdata/markdown-marked-golden.jsonl
import assert from "node:assert/strict";
import { readFileSync, realpathSync } from "node:fs";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

const root = process.env.PI_PACKAGE_ROOT ?? realpathSync("extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent");
assert.equal(JSON.parse(readFileSync(join(root, "package.json"), "utf8")).version, "1.1.0");
assert.equal(JSON.parse(readFileSync(join(root, "node_modules/marked/package.json"), "utf8")).version, "18.0.11");
const dist = join(root, "node_modules/@earendil-works/pi-tui/dist");
const { Markdown } = await import(pathToFileURL(join(dist, "components/markdown.js")).href);
const { setCapabilities } = await import(pathToFileURL(join(dist, "terminal-image.js")).href);
const { Chalk } = await import(pathToFileURL(join(root, "node_modules/chalk/source/index.js")).href);
const chalk = new Chalk({ level: 3 });
const theme = {
	heading: (s) => chalk.bold.cyan(s),
	link: (s) => chalk.blue(s),
	linkUrl: (s) => chalk.dim(s),
	code: (s) => chalk.yellow(s),
	codeBlock: (s) => chalk.green(s),
	codeBlockBorder: (s) => chalk.dim(s),
	quote: (s) => chalk.italic(s),
	quoteBorder: (s) => chalk.dim(s),
	hr: (s) => chalk.dim(s),
	listBullet: (s) => chalk.cyan(s),
	bold: (s) => chalk.bold(s),
	italic: (s) => chalk.italic(s),
	strikethrough: (s) => chalk.strikethrough(s),
	underline: (s) => chalk.underline(s),
};
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
for (const c of JSON.parse(readFileSync("tui/testdata/markdown-marked-corpus.json", "utf8"))) {
	console.log(JSON.stringify({ name: c.name, lines: new Markdown(c.source, 0, 0, theme).render(c.width) }));
}
