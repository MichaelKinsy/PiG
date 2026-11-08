// Drive the installed pi-tui Markdown with a default text style whose color function counts its calls, so every row records how often (and in which
// order) markdown.ts applied the default style and computed its prefix across render, setText and invalidate.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (p) => import(pathToFileURL(root + p));
const { Markdown: pinnedMarkdown, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const Markdown = pinnedMarkdown;
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;
const tag = (name) => (text) => `<${name}>${text}</${name}>`;
const taggedTheme = {
  heading: tag("h"), link: tag("a"), linkUrl: tag("u"), code: tag("c"), codeBlock: tag("cb"), codeBlockBorder: tag("cbb"),
  quote: tag("q"), quoteBorder: tag("qb"), hr: tag("hr"), listBullet: tag("lb"), bold: tag("b"), italic: tag("i"), strikethrough: tag("s"), underline: tag("ul"),
};
const results = JSON.parse(input).map((probe) => {
  setCapabilities({ images: null, trueColor: true, hyperlinks: false });
  let calls = 0;
  const style = { bold: probe.bold, italic: probe.italic, strikethrough: probe.strikethrough, underline: probe.underline };
  if (probe.color) style.color = (text) => `<dc${++calls}>${text}</dc>`;
  const md = new Markdown(probe.text, probe.paddingX, 0, { ...taggedTheme }, style);
  const renders = [];
  for (const op of probe.ops) {
    switch (op.op) {
      case "render": renders.push({ rows: md.render(op.width), calls }); break;
      case "setText": md.setText(op.text); break;
      case "invalidate": md.invalidate(); break;
      default: throw new Error("unknown op " + op.op);
    }
  }
  return renders;
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
