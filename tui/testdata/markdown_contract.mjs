// Drive the installed pi-tui Markdown through the constructor arguments the contract tests vary (theme, default text style, options, hyperlink
// capability, code highlighting and indent) and sequences of render, setText and invalidate calls, recording the rows of every render and the
// callbacks it made (transform, background, highlight), never a translated oracle.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (p) => import(pathToFileURL(root + p));
const { Markdown: pinnedMarkdown, setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const Markdown = pinnedMarkdown;
const { initTheme, getMarkdownTheme } = await load("dist/modes/interactive/theme/theme.js");
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const tag = (name) => (text) => `<${name}>${text}</${name}>`;
const taggedTheme = {
  heading: tag("h"), link: tag("a"), linkUrl: tag("u"), code: tag("c"), codeBlock: tag("cb"), codeBlockBorder: tag("cbb"),
  quote: tag("q"), quoteBorder: tag("qb"), hr: tag("hr"), listBullet: tag("lb"), bold: tag("b"), italic: tag("i"), strikethrough: tag("s"), underline: tag("ul"),
};
const transforms = {
  append: (markdown, width) => `${markdown}\n\n[${width}]`,
  empty: () => "",
  blank: () => "  \n",
  width: (markdown, width) => `${width}`,
};
const results = JSON.parse(input).map((probe) => {
  const log = { transform: [], bg: 0, highlight: [] };
  // initTheme reads the color depth the capabilities report, so they are set first: a probe must not depend on the one before it, or the split of a corpus into several node processes changes the first probe of each.
  setCapabilities({ images: null, trueColor: true, hyperlinks: probe.links });
  let theme;
  if (probe.theme === "tagged") {
    theme = { ...taggedTheme };
    if (probe.highlight) {
      theme.highlightCode = (code, lang) => {
        log.highlight.push([code, lang ?? ""]);
        return code.split("\n").map((line) => `<hl ${lang ?? ""}>${line}</hl>`);
      };
    }
    if (probe.indent !== null) theme.codeBlockIndent = probe.indent;
  } else {
    initTheme(probe.theme);
    theme = getMarkdownTheme();
  }
  let style;
  if (probe.style) {
    style = { bold: probe.style.bold, italic: probe.style.italic, strikethrough: probe.style.strikethrough, underline: probe.style.underline };
    if (probe.style.color) style.color = tag("dc");
    if (probe.style.bg) style.bgColor = (text) => { log.bg++; return `<bg>${text}</bg>`; };
  }
  const options = { preserveOrderedListMarkers: probe.options.preserveOrderedListMarkers, preserveBackslashEscapes: probe.options.preserveBackslashEscapes };
  if (probe.options.renderLatex !== null) options.renderLatex = probe.options.renderLatex;
  if (probe.options.transform) {
    const transform = transforms[probe.options.transform];
    options.transform = (markdown, width) => {
      log.transform.push([markdown, width]);
      return transform(markdown, width);
    };
  }
  const md = new Markdown(probe.text, probe.paddingX, probe.paddingY, theme, style, options);
  const renders = [];
  for (const op of probe.ops) {
    switch (op.op) {
      case "render": {
        const rows = md.render(op.width);
        renders.push({ rows, log: structuredClone(log) });
        log.transform = [];
        log.bg = 0;
        log.highlight = [];
        break;
      }
      case "setText": md.setText(op.text); break;
      case "invalidate": md.invalidate(); break;
      default: throw new Error("unknown op " + op.op);
    }
  }
  return renders;
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
