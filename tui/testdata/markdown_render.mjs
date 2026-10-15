// Drive the installed pi-tui Markdown, never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { Markdown, setCapabilities } = await import(pathToFileURL(root + "node_modules/@earendil-works/pi-tui/dist/index.js"));
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const tag = (name) => (text) => `<${name}>${text}</${name}>`;
const theme = {
  heading: tag("h"), link: tag("a"), linkUrl: tag("u"), code: tag("c"), codeBlock: tag("cb"), codeBlockBorder: tag("cbb"),
  quote: tag("q"), quoteBorder: tag("qb"), hr: tag("hr"), listBullet: tag("lb"), bold: tag("b"), italic: tag("i"), strikethrough: tag("s"), underline: tag("ul"),
};
const results = JSON.parse(input).map((probe) => {
  const md = new Markdown(probe.text, probe.paddingX, probe.paddingY, theme);
  return probe.widths.map((width) => md.render(width));
});
process.stdout.write(JSON.stringify(results));
