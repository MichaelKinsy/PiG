// Keep Theme initialization independent of the TUI barrel and highlighter. Only private import edges change; Pi's exported classes, theme state and synchronous helper bodies stay intact.
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, relative } from "node:path";

const theme = join(process.argv[2], "modes/interactive/theme/theme.js");
let source = readFileSync(theme, "utf8");
for (const [before, after] of [
  ['import { getCapabilities, } from "../../../../../pi-tui.mjs";', 'import { getCapabilities, } from "../../../../pi-tui/terminal-image.js";'],
  ['import { highlight, supportsLanguage } from "../../../utils/syntax-highlight.js";', 'import { highlight, supportsLanguage } from "../../../../../syntax-highlight.mjs";'],
]) {
  if (source.split(before).length !== 2) throw new Error(`Pi startup import changed: ${before}`);
  source = source.replace(before, after);
}
writeFileSync(theme, source);

// All coding-agent and agent consumers use the same AI core/catalog entries as the extension's virtual modules.
for (const root of [process.argv[2], join(dirname(process.argv[2]), "pi-agent-core")]) {
  for (const file of readdirSync(root, { recursive: true }).filter(file => file.endsWith(".js") && !file.replaceAll("\\", "/").startsWith("sdk-bundle/"))) {
    const path = join(root, file);
    const original = readFileSync(path, "utf8");
    const rewritten = original.replaceAll('/pi-ai/index.js"', '/pi-ai/sdk-bundle/index.js"').replaceAll('/pi-ai/compat.js"', '/pi-ai/sdk-bundle/compat.js"').replaceAll('/pi-ai/providers/all.js"', '/pi-ai/sdk-bundle/providers.js"');
    if (rewritten !== original) writeFileSync(path, rewritten);
  }
}

// The expensive RGI Unicode set is private and needed only by the guarded emoji-width branch. Its native RegExp and test semantics stay in one deferred module.
const tui = join(dirname(process.argv[2]), "pi-tui", "utils.js");
const utils = readFileSync(tui, "utf8");
const literal = utils.match(/^const rgiEmojiRegex = (.+);$/m);
if (!literal) throw new Error("Pi RGI emoji regex declaration changed");
writeFileSync(join(dirname(dirname(process.argv[2])), "pi-tui-emoji.mjs"), `export const rgiEmojiRegex = ${literal[1]};\n`);
writeFileSync(tui, utils.replace(literal[0], 'import { rgiEmojiRegex } from "../../pi-tui-emoji-lazy.mjs";'));

const tuiRoot = dirname(tui);
let lazyUtils = readFileSync(tui, "utf8");
const nativeSegmenters = 'const graphemeSegmenter = new Intl.Segmenter(undefined, { granularity: "grapheme" });\nconst wordSegmenter = new Intl.Segmenter(undefined, { granularity: "word" });';
if (!lazyUtils.includes(nativeSegmenters)) throw new Error("Pi shared segmenter construction changed");
lazyUtils = lazyUtils.replace(nativeSegmenters, 'import { graphemeSegmenter, wordSegmenter, getGraphemeSegmenter as nativeGraphemeSegmenter, getWordSegmenter as nativeWordSegmenter } from "../../pi-tui-segmenters.mjs";')
  .replace('return graphemeSegmenter;', 'return nativeGraphemeSegmenter();')
  .replace('return wordSegmenter;', 'return nativeWordSegmenter();');
writeFileSync(tui, lazyUtils);
for (const file of readdirSync(tuiRoot, { recursive: true }).filter(file => file.endsWith(".js") && !file.replaceAll("\\", "/").startsWith("sdk-bundle/"))) {
  const path = join(tuiRoot, file);
  const original = readFileSync(path, "utf8");
  const specifier = relative(dirname(path), join(dirname(dirname(process.argv[2])), "pi-tui-segmenters.mjs")).replaceAll("\\", "/");
  const rewritten = original.replace(/^const (\w+) = get(Grapheme|Word)Segmenter\(\);$/gm, (_line, name, kind) => {
    const binding = kind.toLowerCase() + "Segmenter";
    return `import { ${binding}${name === binding ? "" : " as " + name} } from ${JSON.stringify(specifier)};`;
  });
  if (rewritten !== original) writeFileSync(path, rewritten);
}
