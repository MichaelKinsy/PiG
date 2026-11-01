// theme.ts getMarkdownTheme() of the installed Pi, called on adversarial text (nested SGR, newlines, empty).
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const load = (p) => import(pathToFileURL(root + p));
const { setCapabilities } = await load("node_modules/@earendil-works/pi-tui/dist/index.js");
const { initTheme, getMarkdownTheme } = await load("dist/modes/interactive/theme/theme.js");
setCapabilities({ images: null, trueColor: true, hyperlinks: false });
let input = "";
process.stdin.setEncoding("utf8"); // decode across chunk boundaries: `input += chunk` on Buffers splits a multi-byte character at a 64 KiB edge
for await (const chunk of process.stdin) input += chunk;
const fields = ["heading", "link", "linkUrl", "code", "codeBlock", "codeBlockBorder", "quote", "quoteBorder", "hr", "listBullet", "bold", "italic", "underline", "strikethrough"];
const results = JSON.parse(input).map((probe) => {
  initTheme(probe.theme);
  const md = getMarkdownTheme();
  const out = {};
  for (const field of fields) out[field] = md[field](probe.text);
  out.highlight = md.highlightCode(probe.text, probe.lang ?? undefined);
  return out;
});
process.stdout.write(JSON.stringify(results), () => process.exit(0));
