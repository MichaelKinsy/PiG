// Generates corpus.jsonl: each line {"in": <text>, "out": <JSON.stringify(JSON.parse(in))> | null}.
// Run: node testdata/gen.mjs > testdata/corpus.jsonl   (Node 26, JSON.parse and JSON.stringify are the oracle)
let seed = 12345;
const rnd = () => { seed = (seed * 1664525 + 1013904223) >>> 0; return seed / 4294967296; };
const pick = (a) => a[Math.floor(rnd() * a.length)];
const keys = ["a", "b", "role", "0", "1", "10", "2", "01", "4294967294", "4294967295", "x y", "é", "\u2028", "__proto__", "constructor", "", "-1", "1.5", "z"];
const numbers = [0, -0, 1, -1, 1.5, 0.1, 1e21, 1e-7, 123456789012345680000, 1e300, 5e-324, 0.30000000000000004, 9007199254740993, -1e-7, 100, 1e20, 12345.678e10, 2 ** 53];
const strs = ["", "a", "he\"llo", "back\\slash", "tab\t", "nl\n", "\u0000", "\u001f", "\u007f", "é", "日本", "😀", "\ud800", "\udc00", "\ud83d", "a\ude00b", "\u2028\u2029", "/", "\b\f\r"];
function gen(d) {
  const r = rnd();
  if (d > 4 || r < 0.35) {
    const k = rnd();
    if (k < 0.15) return null;
    if (k < 0.3) return rnd() < 0.5;
    if (k < 0.6) return pick(numbers);
    return pick(strs);
  }
  if (r < 0.65) { const n = Math.floor(rnd() * 5); return Array.from({ length: n }, () => gen(d + 1)); }
  const o = {}; const n = Math.floor(rnd() * 6);
  for (let i = 0; i < n; i++) Object.defineProperty(o, pick(keys), { value: gen(d + 1), enumerable: true, writable: true, configurable: true });
  return o;
}
// raw text generator: re-spells values with random whitespace, escapes and duplicate keys
function spell(v) {
  if (v === null || typeof v === "boolean") return String(v);
  if (typeof v === "number") { const s = String(v); return Object.is(v, -0) ? "-0" : rnd() < 0.2 && Number.isInteger(v) && Math.abs(v) < 1e15 ? `${v}.0e0` : s; }
  if (typeof v === "string") {
    let o = '"';
    for (const ch of v) {
      const c = ch.codePointAt(0);
      if (rnd() < 0.3 || (c >= 0xD800 && c <= 0xDFFF)) { for (let i = 0; i < ch.length; i++) o += "\\u" + ch.charCodeAt(i).toString(16).padStart(4, rnd() < 0.5 ? "0" : "0").toUpperCase(); }
      else if (c < 0x20 || ch === '"' || ch === "\\") o += c === 10 ? "\\n" : c === 34 ? '\\"' : c === 92 ? "\\\\" : "\\u" + c.toString(16).padStart(4, "0");
      else o += ch;
    }
    return o + '"';
  }
  const ws = () => pick(["", " ", "\n", "\t ", "\r\n"]);
  if (Array.isArray(v)) return "[" + ws() + v.map(spell).join(ws() + "," + ws()) + ws() + "]";
  const parts = Object.keys(v).map((k) => spell(k) + ws() + ":" + ws() + spell(v[k]));
  if (parts.length && rnd() < 0.2) parts.push(parts[0].replace(/:.*$/s, ":" + spell(gen(3)))); // duplicate key, later value wins
  return "{" + ws() + parts.join(ws() + "," + ws()) + ws() + "}";
}
const out = [];
const emit = (text) => { let r = null; try { r = JSON.stringify(JSON.parse(text)); } catch {} out.push(JSON.stringify({ in: text, out: r === undefined ? null : r })); };
for (let i = 0; i < 1500; i++) emit(spell(gen(0)));
for (const t of ['{"a":1,"a":2}', '{"b":1,"a":2,"b":3}', '{"2":1,"1":2,"a":3,"0":4}', "[1,]", "{,}", "01", "1.", ".5", "-", "+1", "1e", "1e999", "-1e999", '"\\x"', '"\\u12"', '"\t"', "[", "nul", "true false", " 1 ", '"\\ud83d\\ude00"', '"\\ud83d"', '"\\ude00\\ud83d"', '"\\ud83d\\u0041"', "NaN", "Infinity", "0.0000001", "123456789012345678901234567890", "-0", "[-0]", "1E5", "1e+5", "1E-5", "{\"__proto__\":{\"a\":1}}"]) emit(t);
console.log(out.join("\n"));
