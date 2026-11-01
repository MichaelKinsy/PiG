// Generates testdata/golden_regex.json and testdata/golden_property.json from this Node's own RegExp (Pi runs the same engine).
// Run with plain Node 24 from this directory: node gen/golden.mjs
// Pi commit 6d4b7443 (packages/schema/src/shared.ts): sharedRegex tries new RegExp(source, "u"), then new RegExp(source), then calls test().
import { readFileSync, writeFileSync } from "node:fs";
import { binaryProperties, discoverScriptAliases, generalCategories, scriptLongNames } from "./names.mjs";
import { randomNamePatterns, randomPatterns } from "./random.mjs";
import { rangesOf } from "./unicode.mjs";

const here = (p) => new URL(p, import.meta.url);

// Lone surrogates: a Go string holds one as WTF-8, and JSON.stringify writes it as an escape that the Go test decodes back to WTF-8.
const loneSurrogates = ["\ud800", "\udc00", "\ud83d", "\ude00", "\ud83da", "a\ude00", "\ud83d\ud83d", "\ude00\ud83d", "\ud83d\ude00\ud83d", "\ude00\ud83d\ude00", "😀\ud83d", "\ude00😀", "\ud83d😀", "😀\ude00", "x\ud83dx", "x\ude00x", "\ud83d\n", "\ud7ff\ude00", "\udbff\udc00", "\udbff\ud7ff"];

// The pool holds the inputs every pattern is tried against: short strings over a small alphabet (backreference behavior), line terminators, and one character of each interesting Unicode class.
const pool = ["", "a", "b", "ab", "abc", "aaa", "abab", "A", "Z", "z", "0", "5", "9", "_", "-", " ", "\t", "\n", "\r", "\r\n", "a\n", "\na", "a b", "\u2028", "\u2029", "\u00a0", "\ufeff", "\u0085", "é", "É", "ß", "ǅ", "ʰ", "ª", "٣", "Ⅷ", "²", "(", ")", "[", "]", "{", "}", "|", ".", "*", "+", "?", "^", "$", "\\", "/", "'", "«", "¬", "©", "\u0300", "\u0903", "\u20dd", "\u00ad", "\u0000", "\u0008", "\ue000", "\uffff", "α", "я", "ء", "א", "한", "あ", "ア", "漢", "ก", "ह", "ა", "😀", "😀😀", "🐲", "🐲🐲", "🐲a", "a🐲", "\u{10000}", "\u{1d49c}", "\u{10ffff}", "x😀x", "a😀a", "1😀1", "\u017f", "\u212a", "İ", "ı", "ǅ", "Ǆ", "ǆ", "ß", "ẞ", "σ", "ς", "Σ", "ᾀ", "ᾈ", "Ω", "ⅰ", "Ⅰ", "ⓐ", "Ⓐ", "\u{10400}", "\u{10428}", "ﬀ", "\u0345", "\u0399", "\u03b9", "\u1fbe", "\u212b", "å", "Å", "SS", "ss", "kK", "sſ", "s😀", "σς", "ςσ", "A\u03c3a\u00b5", "\u212ak", "iİ", "İi", "s\u017f", "k\u212a", "\u212ak", "foo", "foobar", "bar", "foo\nbar", "foo\n", "aa", "aba", "abcabc", "abcd", "aXb", "x=1;y=2", "1234", "12", "12-12", "ab12", "AB", "aB", "xyz", "a-a", "ab ab", "x1", "11", "aabb", "baab", "abba", "bab", "c", "d", "dd", "x", "y", "xx", "{", "{}", "{1}", "a{1}", "x{", "}x", "]x"];
// V8 stores a string whose code units are all at most U+00FF with one byte per unit, and a modifier group that ignores case sees such a string differently from a wider one, so the pool makes every such string a one-byte string whatever way it was built.
function normalizePool() {
  for (let i = 0; i < pool.length; i++) {
    if ([...pool[i]].every((c) => c.codePointAt(0) <= 0xff)) pool[i] = Buffer.from(pool[i], "latin1").toString("latin1");
  }
}

pool.push(...loneSurrogates);

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

// The patterns are the hand-written ones of patterns.txt 1600 random ones and 600 random nestings of repeated group names, all from fixed seeds.
normalizePool();
// Patterns with lone surrogates in the source; a pattern file is UTF-8 text and cannot hold them.
const surrogatePatterns = [
  // The source holds the lone surrogates themselves.
  "\ud83d", "\ude00", "\ud83d\ude00", "\ude00\ud83d", "[\ud800-\udfff]", "^[\ud800-\udfff]$", "[^\ud800-\udfff]", "[\ud83d\ude00]", "^[\ud83d]$", "^[\ude00]$", "[^\ud83d]", "[^\ude00]", "\ud83d+", "\ude00+", "^\ud83d$", "^\ude00$", "(?:\ud83d)(?:\ude00)", "(?<=\ud83d)x", "x(?=\ude00)", "(?<!\ud83d)\ude00", "\ud83d(?!\ude00)", "(?<=\ud83d)", "(?=\ude00)", "(?:\ud83d|\ude00)", "\ud83d|\ude00", "(\ud83d)\\1", "(?i:\ud83d)", "(?i:[\ud83d])", "[\ud83d-\ude00]", "[\ude00-\ud83d]", "\ud83d{2}", "^\ud83d\ude00{2}$", "x\ud83d\\u0061", "[\ud83d\ude00-\ud83e]",
  // The source spells them as escapes or has none.
  ...[String.raw`^.$`, String.raw`^..$`, String.raw`^.*$`, String.raw`\B`, String.raw`\b`, String.raw`^\W$`, String.raw`^\D$`, String.raw`^\S$`, String.raw`\p{Cs}`, String.raw`^\p{Cs}$`, String.raw`^\P{Cs}$`, String.raw`\p{Any}`, String.raw`^\p{Any}$`, String.raw`^\p{Any}\p{Any}$`, String.raw`\u{d83d}`, String.raw`\u{de00}`, String.raw`\u{d83d}\u{de00}`, String.raw`[\u{d83d}-\u{de00}]`, String.raw`[\ud83d-\ude00]`, String.raw`\x41\ud83d`, String.raw`[\u0000-\uffff]`, String.raw`[\u0000-\u{10ffff}]`, String.raw`^[\u0000-\u{10ffff}]$`, String.raw`(.)\1`, String.raw`(.)(.)\2\1`, "[😀-😎]", "[^😀]", "^[^😀]$", String.raw`^.{2}$`, String.raw`^.{3}$`, String.raw`^[\ud800-\udfff]$`, String.raw`[^\ud800-\udfff]`, String.raw`^\ud83d$`, String.raw`^\ude00$`, String.raw`(?<=\ud83d)x`, String.raw`x(?=\ude00)`, String.raw`(?<!\ud83d)\ude00`, String.raw`\ud83d(?!\ude00)`, String.raw`(?<=\ud83d)`, String.raw`(?=\ude00)`, String.raw`(?<=\ud83d)\ude00`, String.raw`(?<![\ud83d])[\ude00]`, String.raw`^(?:.|..)$`, String.raw`^(?:\W)+$`, String.raw`^[^\W]$`, String.raw`^\W\W$`],
];
const patterns = [
  ...readFileSync(here("./patterns.txt"), "utf8")
    .split("\n")
    .filter((l) => l !== "" && !l.startsWith("#")),
  ...surrogatePatterns,
  ...randomPatterns(20260101, 1600),
  ...randomNamePatterns(20260102, 600),
];

// packBits writes booleans as hex digits, four to a digit, the first boolean in the high bit of the first digit; the last digit is padded with zeros.
function packBits(bits) {
  let out = "";
  for (let i = 0; i < bits.length; i += 4) {
    let nibble = 0;
    for (let j = 0; j < 4; j++) nibble = (nibble << 1) | (bits[i + j] ? 1 : 0);
    out += nibble.toString(16);
  }
  return out;
}

// attempt returns the RegExp, or the message of the SyntaxError that new RegExp throws.
function attempt(source, flags) {
  try {
    return { re: new RegExp(source, flags) };
  } catch (e) {
    return { message: e.message };
  }
}

const rows = patterns.map((p) => {
  const row = { p };
  for (const [key, flags] of [["u", "u"], ["a", ""]]) {
    const { re, message } = attempt(p, flags);
    row[key] = re === undefined ? null : packBits(pool.map((s) => re.test(s)));
    if (re === undefined) row[key + "e"] = message;
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

// golden_case.json lists, for the code units and the code points that V8 matches without regard to case, the characters that match each other: the "classes" of RegExp's i flag, with the u flag and without it.
function caseClasses(flags, limit) {
  let str = "";
  const step = 0x1000;
  for (let cp = 0; cp < limit; cp += step) {
    const chunk = [];
    for (let c = cp; c < Math.min(cp + step, limit); c++) if (c < 0xd800 || c > 0xdfff) chunk.push(c);
    str += String.fromCodePoint(...chunk);
  }
  const chars = [...str];
  // Candidates are the characters that have a case mapping of any kind, which every class member has.
  const cased = chars.filter((ch) => {
    const lower = ch.toLowerCase();
    const upper = ch.toUpperCase();
    return lower !== ch || upper !== ch || ch.toLocaleUpperCase() !== ch;
  });
  const classes = new Map();
  for (const ch of cased) {
    const key = ch.codePointAt(0);
    if (classes.has(key)) continue;
    const re = new RegExp(`${ch.replace(/[\\^$.*+?()[\]{}|/]/g, "\\$&")}`, flags + "gi");
    const members = [];
    // Scan every character once for the ones this class member matches.
    for (const m of str.matchAll(re)) members.push(m[0].codePointAt(0));
    if (members.length > 1 || members[0] !== key) {
      const sorted = [...new Set(members)].sort((a, b) => a - b);
      for (const m of sorted) classes.set(m, sorted);
    }
  }
  return [...new Set([...classes.values()].map((c) => c.join(",")))].map((c) => c.split(",").map(Number));
}
writeFileSync(here("../testdata/golden_case.json"), JSON.stringify({ node: process.versions.node, unicodeFlag: caseClasses("u", 0x110000), plain: caseClasses("", 0x10000) }) + "\n");
