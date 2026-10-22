import { SAMPLES, THEMES, useTheme } from "../../../../../automation/gen/sdk-vector-lib/theme-state.mjs";

// Highlighted through getMarkdownTheme().highlightCode: a language, none, an unknown one, an empty code, languages with several scopes.
const HIGHLIGHT = [
  ["const x = 1;\nfunction f() { return \"s\"; } // c", "javascript"],
  ["def f(a):\n    return a  # c\n", "python"],
  ["plain text, no language", undefined],
  ["x", "no-such-language"],
  ["fn main() { let v: Vec<u8> = vec![1]; }\n", "rust"],
  ["{\"a\": [1, true, null], \"b\": \"s\"}", "json"],
  ["<div class=\"a\">&amp;</div>", "html"],
  ["", "javascript"],
  ["# head\n**bold** `code` [l](http://x)", "markdown"],
  ["package main\n\nfunc main() { println(\"hi\") }", "go"],
  ["SELECT a, 1 FROM t WHERE b = 'x';", "sql"],
  ["echo $HOME # x\nif [ -f y ]; then ls; fi", "bash"],
  ["--- a\n+++ b\n+added\n-removed\n context", "diff"],
  ["a\nb", ""],
];

export const cases = THEMES.map(([name, mode]) => ({ name: `${name} ${mode}`, args: [name, mode] }));

export async function call(fn, themeName, colorMode) {
  const palette = await useTheme(this, themeName, colorMode);
  const theme = fn();
  const styles = {};
  for (const [key, style] of Object.entries(theme)) if (key !== "highlightCode") styles[key] = SAMPLES.map((text) => style(text));
  const highlight = HIGHLIGHT.map(([code, lang]) => ({ code, lang, lines: theme.highlightCode(code, lang) }));
  return { palette, keys: Object.keys(theme), styles, highlight };
}
