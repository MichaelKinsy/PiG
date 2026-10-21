import { THEMES, useTheme } from "../../../../../automation/gen/sdk-vector-lib/theme-state.mjs";

const CODE = [
  ["const x = 1;\nfunction f() { return \"s\"; } // c", "javascript"],
  ["const x: number = 1;\nclass A<T> { y?: T }", "typescript"],
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
  ["tab\there \u4e2d\u6587 \u{1f600}", "javascript"],
];

export const cases = THEMES.flatMap(([name, mode]) => CODE.map(([code, lang], i) => ({ name: `${name} ${mode} #${i} ${lang ?? "no language"}`, args: [code, lang, name, mode] })));

export async function call(fn, code, lang, themeName, colorMode) {
  const palette = await useTheme(this, themeName, colorMode);
  return { palette, lines: fn(code, lang) };
}
