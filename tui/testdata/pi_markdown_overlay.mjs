// Pi main 54280c4ff (fix(tui): hide HTML comments in Markdown rendering, #11755) applied to the pinned pi-tui Markdown (D265), for the oracle
// drivers that compare Markdown rendering. Each replacement must find its anchor exactly once, so the overlay fails when the pin moves
// and the divergence can be removed. Set PIG_PI_MARKDOWN=pinned to get the pinned Markdown unchanged (TestMarkdownHTMLCommentsDifferFromPinnedPi).
import { mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";

export async function piMarkdown(root, pinnedMarkdown) {
  return process.env.PIG_PI_MARKDOWN === "pinned" ? pinnedMarkdown : markdownWithHtmlComments(root);
}

export async function markdownWithHtmlComments(root) {
  const dist = root + "node_modules/@earendil-works/pi-tui/dist/components/";
  let source = readFileSync(dist + "markdown.js", "utf8");
  const replace = (from, to) => {
    if (source.split(from).length !== 2) throw new Error("html-comments overlay: anchor not found exactly once: " + from);
    source = source.replace(from, () => to);
  };
  const packageEntry = (name) => {
    const dir = root + "node_modules/" + name + "/";
    return pathToFileURL(dir + JSON.parse(readFileSync(dir + "package.json", "utf8")).exports["."].default).href;
  };
  replace('from "marked";', `from "${packageEntry("marked")}";`);
  replace('from "marked-cjk-friendly";', `from "${packageEntry("marked-cjk-friendly")}";`);
  source = source.replace(/from "(\.\.\/[^"]+)";/g, (_, path) => `from "${new URL(path, pathToFileURL(dist)).href}";`);
  replace("const markdownParser = new Marked();", `// An unclosed comment runs to the end of an HTML block, as in CommonMark, so streamed comments stay hidden.
const HTML_COMMENT_REGEX = /<!--[\\s\\S]*?(?:-->|$)/g;
function stripHtmlComments(html) {
    return html.replace(HTML_COMMENT_REGEX, "");
}
function removeHtmlCommentBlocks(tokens) {
    for (let i = tokens.length - 1; i >= 0; i--) {
        const token = tokens[i];
        if (token.type === "list") {
            for (const item of token.items)
                removeHtmlCommentBlocks(item.tokens);
        }
        else if (token.type === "blockquote") {
            removeHtmlCommentBlocks(token.tokens ?? []);
        }
        else if (token.type === "html" && stripHtmlComments(token.raw).trim() === "") {
            tokens.splice(i, 1);
            if (tokens[i]?.type === "space" && (i === 0 || tokens[i - 1]?.type === "space")) {
                tokens.splice(i, 1);
            }
            else if (i === tokens.length && tokens[i - 1]?.type === "space") {
                tokens.splice(--i, 1);
            }
        }
    }
}
const markdownParser = new Marked();`);
  replace("trimPartialClosingFences(tokens);\n            this.cachedTokens", "trimPartialClosingFences(tokens);\n            removeHtmlCommentBlocks(tokens);\n            this.cachedTokens");
  replace("lines.push(this.applyDefaultStyle(token.raw.trim()));", "lines.push(this.applyDefaultStyle(stripHtmlComments(token.raw).trim()));");
  replace("        for (const token of tokens) {\n            switch (token.type) {\n                case \"latex\": {", `        let dropLeadingSpace = false;
        for (let i = 0; i < tokens.length; i++) {
            let token = tokens[i];
            if (dropLeadingSpace && token.type === "text" && !token.tokens?.length && /^[ \\t]/.test(token.text)) {
                token = { ...token, text: token.text.replace(/^[ \\t]+/, "") };
            }
            dropLeadingSpace = false;
            switch (token.type) {
                case "latex": {`);
  replace(`                    // Render inline HTML as plain text
                    if ("raw" in token && typeof token.raw === "string") {
                        result += applyTextWithNewlines(token.raw);
                    }`, `                    if (token.raw.startsWith("<!--")) {
                        dropLeadingSpace = i === 0 || /[ \\t]$/.test(tokens[i - 1].raw);
                    }
                    else {
                        result += applyTextWithNewlines(token.raw);
                    }`);
  const file = join(mkdtempSync(join(tmpdir(), "pi-md-html-comments-")), "markdown.mjs");
  writeFileSync(file, source);
  return (await import(pathToFileURL(file).href)).Markdown;
}
