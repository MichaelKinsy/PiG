// Renders upstream components over argv[2] (JSON {content, widths}) and
// prints {text, truncated, markdown} as width -> rows maps. Used by
// tui/component_width_sweep_test.go. argv[3] is the directory holding a
// node_modules with get-east-asian-width and marked linked in.
import fs from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
const base = path.resolve(process.argv[3]);
// import() takes a URL; on Windows an absolute path parses as a "c:" scheme.
const load = (rel) => import(pathToFileURL(path.join(base, rel)).href);
const { Text } = await load("components/text.ts");
const { TruncatedText } = await load("components/truncated-text.ts");
const { Markdown } = await load("components/markdown.ts");
const { visibleWidth } = await load("utils.ts");
const input = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const id = (s) => s;
const theme = { heading: id, link: id, linkUrl: id, code: id, codeBlock: id, codeBlockBorder: id, quote: id, quoteBorder: id, hr: id, listBullet: id, bold: id, italic: id, strikethrough: id, underline: id };
const all = input.content.join("\n\n");
const out = { text00: {}, text11: {}, truncated00: {}, truncated11: {}, markdownOverflow: {} };
for (const w of input.widths) {
	out.text00[w] = new Text(all, 0, 0).render(w);
	out.text11[w] = new Text(all, 1, 1).render(w);
	out.truncated00[w] = new TruncatedText(all, 0, 0).render(w);
	out.truncated11[w] = new TruncatedText(all, 1, 1).render(w);
	out.markdownOverflow[w] = new Markdown(all, 0, 0, theme).render(w).filter((l) => visibleWidth(l) > w).length;
}
process.stdout.write(JSON.stringify(out));
