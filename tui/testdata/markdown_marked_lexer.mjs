// Record the block tokens that pi-tui's Markdown component reads from marked, never a translated oracle. Marked.prototype.lexer is patched before Pi renders anything: every call of
// markdownParser.lexer(text) (the one Marked instance of packages/tui/src/components/markdown.ts) serializes the token tree it returns, before trimPartialClosingFences changes it. The
// instance is captured from the first call, so a text that Markdown.render does not lex (an empty or blank one) is lexed through the same instance, with the same normalization
// (tabs as three spaces, markdown.ts render) that render applies. A source whose index is a multiple of the request's `render` is also rendered by a Markdown component, and the
// script fails unless render lexed exactly that normalized text and the tree it recorded equals the one the direct call returned.
//
// The canonical form is the one tui/markdown_marked_lexer_oracle_test.go builds from PiG's tokens: s-expressions of the fields the renderer reads, strings quoted with only \" \\ and
// \xHH escapes (a lone surrogate is U+FFFD, as UTF-8 encodes it). The inline token lists are not recorded; the source each list was lexed from is: the raw of its tokens joined, which
// is what Lexer.inlineQueue held for it. A task item's checkbox is the one token PiG does not materialize (it keeps the state in the item): marked adds a checkbox token to the item
// (or to the inline tokens of its first paragraph, with its raw ahead of that paragraph's raw and text), the form below drops it after checking it is where marked put it.
import { readFileSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
process.env.FORCE_COLOR = "3";
const tuiPath = realpathSync(root + "node_modules/@earendil-works/pi-tui/dist/index.js");
const tuiUrl = pathToFileURL(tuiPath);
const marked = await import(pathToFileURL(createRequire(tuiUrl).resolve("marked")));
const { Markdown } = await import(tuiUrl);
let input = "";
process.stdin.setEncoding("utf8");
for await (const chunk of process.stdin) input += chunk;

const q = (s) => {
  let out = '"';
  for (const ch of String(s)) {
    const c = ch.codePointAt(0);
    if (ch === '"') out += '\\"';
    else if (ch === "\\") out += "\\\\";
    else if (c < 0x20 || c === 0x7f) out += "\\x" + c.toString(16).padStart(2, "0");
    else if (c >= 0xd800 && c <= 0xdfff) out += "\ufffd";
    else out += ch;
  }
  return out + '"';
};
const inlineRaw = (tokens) => q((tokens ?? []).filter((t) => t.type !== "checkbox").map((t) => t.raw).join(""));
const fail = (what, token) => { throw new Error(`${what}: ${JSON.stringify(token)}`); };

function item(it, list) {
  let tokens = it.tokens;
  let raw = it.raw;
  let text = it.text;
  let firstText = null; // the raw and text of the first token without a checkbox
  if (it.checked !== undefined) {
    if (!it.task) fail("checked item that is no task", it);
    if (list.loose) {
      const first = tokens[0];
      const checkbox = first?.tokens?.[0];
      if (checkbox?.type !== "checkbox" || !first.raw.startsWith(checkbox.raw) || !first.text.startsWith(checkbox.raw)) fail("loose task item without its checkbox", it);
      firstText = { raw: first.raw.slice(checkbox.raw.length), text: first.text.slice(checkbox.raw.length) };
    } else {
      if (tokens[0]?.type !== "checkbox") fail("task item without its checkbox", it);
      tokens = tokens.slice(1);
    }
  } else if (it.task) fail("task item without a state", it);
  const children = tokens.map((t, i) => block(t, i === 0 ? firstText : null)).join(" ");
  return `(item ${q(raw)} ${q(text)} ${it.task} ${it.checked === true} ${it.loose} [${children}])`;
}

function block(t, override = null) {
  const raw = override?.raw ?? t.raw;
  const text = override?.text ?? t.text;
  switch (t.type) {
    case "space": return `(space ${q(raw)})`;
    case "hr": return `(hr ${q(raw)})`;
    case "code": return `(code ${q(raw)} ${q(text)} ${q(t.lang ?? "")})`;
    case "heading": return `(heading ${q(raw)} ${q(text)} ${t.depth} ${inlineRaw(t.tokens)})`;
    case "paragraph": return `(paragraph ${q(raw)} ${q(text)} ${inlineRaw(t.tokens)})`;
    case "text": return `(text ${q(raw)} ${q(text)} ${inlineRaw(t.tokens)})`;
    case "html": return `(html ${q(raw)} ${q(text)})`;
    case "def": return `(def ${q(raw)} ${q(t.tag)} ${q(t.href)})`;
    case "latexBlock": return `(latexBlock ${q(raw)} ${q(text)} ${t.pending === true})`;
    case "blockquote": return `(blockquote ${q(raw)} ${q(text)} [${t.tokens.map((c) => block(c)).join(" ")}])`;
    case "list": {
      if (override) fail("list with an override", t);
      return `(list ${q(raw)} ${t.ordered} ${t.ordered ? t.start : 0} ${t.loose} [${t.items.map((i) => item(i, t)).join(" ")}])`;
    }
    case "table": {
      const cell = (c) => `(cell ${q(c.text)} ${inlineRaw(c.tokens)})`;
      return `(table ${q(raw)} [${t.header.map(cell).join(" ")}] [${t.rows.map((r) => `[${r.map(cell).join(" ")}]`).join(" ")}])`;
    }
  }
  return fail("unexpected block token", t);
}

const canonical = (tokens) => {
  const links = Object.entries(tokens.links ?? {}).map(([tag, link]) => `(${q(tag)} ${q(link.href)})`).sort();
  return `${tokens.map((t) => block(t)).join(" ")} {${links.join(" ")}}`;
};

let parser = null;
const recorded = [];
const lexer = marked.Marked.prototype.lexer;
marked.Marked.prototype.lexer = function (src, options) {
  parser ??= this;
  let tokens;
  try {
    tokens = lexer.call(this, src, options);
  } catch (error) {
    recorded.push({ src, form: `THROW ${error.message}` });
    throw error;
  }
  recorded.push({ src, form: canonical(tokens) });
  return tokens;
};

const identity = (text) => text;
const theme = {
  heading: identity, link: identity, linkUrl: identity, code: identity, codeBlock: identity, codeBlockBorder: identity, quote: identity, quoteBorder: identity, hr: identity,
  listBullet: identity, bold: identity, italic: identity, strikethrough: identity, underline: identity,
};
new Markdown("x", 0, 0, theme).render(80);
if (parser === null) throw new Error("Markdown.render did not lex through Marked.prototype.lexer");

const { sources, render } = JSON.parse(input);
const out = sources.map((source, index) => {
  const normalized = source.replace(/\t/g, "   ");
  let form;
  recorded.length = 0;
  try {
    parser.lexer(normalized);
    form = recorded.pop().form;
  } catch (error) {
    form = `THROW ${error.message}`;
  }
  if (render > 0 && index % render === 0 && source.trim() !== "") {
    recorded.length = 0;
    try {
      new Markdown(source, 0, 0, theme).render(40);
    } catch (error) {
      if (!String(error.message).startsWith("Infinite loop")) throw error;
    }
    if (recorded.length === 0 || recorded[0].src !== normalized || recorded[0].form !== form) {
      throw new Error(`render lexed ${JSON.stringify(recorded.map((r) => r.src))} for ${JSON.stringify(normalized)}\n  render: ${recorded[0]?.form}\n  direct: ${form}`);
    }
  }
  return form;
});
process.stdout.write(JSON.stringify(out), () => process.exit(0));
