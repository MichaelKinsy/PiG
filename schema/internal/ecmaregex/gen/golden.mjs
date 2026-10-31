// Generates testdata/golden_regex.json and testdata/golden_property.json from this Node's own RegExp (Pi runs the same engine).
// Run with plain Node 24 from this directory: node gen/golden.mjs
// Pi commit 6d4b7443 (packages/schema/src/shared.ts): sharedRegex tries new RegExp(source, "u"), then new RegExp(source), then calls test().
import { readFileSync, writeFileSync } from "node:fs";
import { binaryProperties, discoverScriptAliases, generalCategories, scriptLongNames } from "./names.mjs";
import { randomPatterns } from "./random.mjs";
import { rangesOf } from "./unicode.mjs";

const here = (p) => new URL(p, import.meta.url);

// The pool holds the inputs every pattern is tried against: short strings over a small alphabet (backreference behavior), line terminators, and one character of each interesting Unicode class.
const pool = ["", "a", "b", "ab", "abc", "aaa", "abab", "A", "Z", "z", "0", "5", "9", "_", "-", " ", "\t", "\n", "\r", "\r\n", "a\n", "\na", "a b", "\u2028", "\u2029", "\u00a0", "\ufeff", "\u0085", "é", "É", "ß", "ǅ", "ʰ", "ª", "٣", "Ⅷ", "²", "(", ")", "[", "]", "{", "}", "|", ".", "*", "+", "?", "^", "$", "\\", "/", "'", "«", "¬", "©", "\u0300", "\u0903", "\u20dd", "\u00ad", "\u0000", "\u0008", "\ue000", "\uffff", "α", "я", "ء", "א", "한", "あ", "ア", "漢", "ก", "ह", "ა", "😀", "😀😀", "🐲", "🐲🐲", "🐲a", "a🐲", "\u{10000}", "\u{1d49c}", "\u{10ffff}", "foo", "foobar", "bar", "foo\nbar", "foo\n", "aa", "aba", "abcabc", "abcd", "aXb", "x=1;y=2", "1234", "12", "12-12", "ab12", "AB", "aB", "xyz", "a-a", "ab ab", "x1", "11", "aabb", "baab", "abba", "bab", "c", "d", "dd", "x", "y", "xx", "{", "{}", "{1}", "a{1}", "x{", "}x", "]x"];
for (const len of [1, 2, 3]) {
  const rec = (s) => {
    if (s.length === len) {
      if (!pool.includes(s)) pool.push(s);
      return;
    }
    for (const c of "abc") rec(s + c);
  };
  rec("");
}

// The patterns are the hand-written ones of patterns.txt and 800 random ones from a fixed seed.
const patterns = [
  ...readFileSync(here("./patterns.txt"), "utf8")
    .split("\n")
    .filter((l) => l !== "" && !l.startsWith("#")),
  ...randomPatterns(20260101, 800),
];

function attempt(source, flags) {
  try {
    return new RegExp(source, flags);
  } catch {
    return null;
  }
}

const rows = patterns.map((p) => {
  const row = { p };
  for (const [key, flags] of [["u", "u"], ["a", ""]]) {
    const re = attempt(p, flags);
    row[key] = re === null ? null : pool.map((s) => (re.test(s) ? "1" : "0")).join("");
  }
  return row;
});
writeFileSync(here("../testdata/golden_regex.json"), JSON.stringify({ node: process.versions.node, v8: process.versions.v8, inputs: pool, rows }) + "\n");

// The property golden maps every property expression V8 accepts to the code point ranges it matches (surrogates are not probed), and lists the expressions V8 rejects.
const { aliases } = discoverScriptAliases();
const accepted = new Map();
const rejected = [];
const sets = [];
const setIds = new Map();
function record(expr) {
  const r = rangesOf(expr);
  if (r === null) {
    rejected.push(expr);
    return;
  }
  const key = r.join(",");
  if (!setIds.has(key)) {
    setIds.set(key, sets.length);
    sets.push(r);
  }
  accepted.set(expr, setIds.get(key));
}
for (const names of generalCategories) {
  for (const name of names) {
    record(name);
    record(`gc=${name}`);
    record(`General_Category=${name}`);
  }
}
for (const names of binaryProperties) for (const name of names) record(name);
const scriptNames = [...scriptLongNames, ...aliases.keys()];
for (const name of scriptNames) {
  for (const prefix of ["sc", "Script", "scx", "Script_Extensions"]) record(`${prefix}=${name}`);
}
// Expressions V8 must reject: the spellings the grammar and the name tables leave out.
for (const expr of ["Latin", "Greek", "Script", "Script=", "=Latin", "sc=latin", "Script=latin", "script=Latin", "SC=Latin", "Script=Foo", "Script=Latin=Latin", "Script_Extension=Latin", "Scx=Latin", "lowercase", "ascii", "any", "l", "LU", "Foo", "gc", "gc=", "gc=Foo", "gc=L=", "General_Category", "General_Category=", "ASCII=Yes", "ASCII=Y", "Lu=Lu", "L=L", "RGI_Emoji", "Basic_Emoji", "Emoji_Keycap_Sequence", "RGI_Emoji_Flag_Sequence", "RGI_Emoji_Modifier_Sequence", "RGI_Emoji_Tag_Sequence", "RGI_Emoji_ZWJ_Sequence", "IsLatin", "InBasicLatin", "Block=Basic_Latin", "Age=1.1", "Line_Break=Alphabetic", "Bidi_Class=L", "Numeric_Type=Decimal", "East_Asian_Width=Wide", "Hyphen", "Other_Alphabetic", "Other_Lowercase", "Other_Math", "ID_Compat_Math_Start", "Prepended_Concatenation_Mark", "Unified_Ideograph=Yes", "Script=Unknown=", "Script=Latn ", " L", "L ", "", "Script_Extensions", "scx", "sc", "Letter ", "gc=letter", "Surrogate_Pair", "Lowercase=True", "ID_Start=", "Alphabetic=Yes"]) {
  if (!accepted.has(expr) && rangesOf(expr) !== null) throw new Error(`expected V8 to reject ${expr}`);
  if (!accepted.has(expr)) rejected.push(expr);
}
writeFileSync(here("../testdata/golden_property.json"), JSON.stringify({ node: process.versions.node, unicode: process.versions.unicode, sets, exprs: Object.fromEntries(accepted), rejected: [...new Set(rejected)] }) + "\n");
console.log(`rows=${rows.length} inputs=${pool.length} exprs=${accepted.size} sets=${sets.length} rejected=${new Set(rejected).size}`);
