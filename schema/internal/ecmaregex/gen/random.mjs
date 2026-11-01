// A seeded generator of random patterns that mixes the pieces of the ECMAScript grammar: atoms, classes, groups of every kind, backreferences, quantifiers and the escapes that differ between the u-mode and the Annex B grammar. Most of its output is invalid in one grammar or both, which is the point.
export function randomPatterns(seed, count) {
  const rnd = () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 4294967296);
  const pick = (a) => a[Math.floor(rnd() * a.length)];
  const literals = ["a", "b", "c", "x", "A", "B", "ſ", "K", "Σ", "σ", "ς", "ß", "İ", "ı", "Ǆ", "ǅ", "Ω", "\\u212a", "\\u017f", "x", "1", " ", "é", "😀", "-", "_", "{", "}", "]", "\\u0061", "\\x62", "\\cJ", "\\0", "\\_", "\\n", "\\t", "\\u{1F600}", "\\/", "\\.", "\\-", "\\ud83d\\ude00", "\\ud83d", "\\1", "\\2", "\\08", "\\a", "\\p{L}", "\\p{Lu}", "\\P{L}", "\\p{Script=Greek}", "\\p{Nd}", "\\k<n1>", "\\k<n2>", "\\k", "\\c", "\\c1", "\\x4", "\\u00"];
  const shorthands = [".", "\\d", "\\w", "\\s", "\\D", "\\W", "\\S"];
  const assertions = ["^", "$", "\\b", "\\B"];
  const quantifiers = ["*", "+", "?", "{1}", "{2}", "{1,2}", "{0,1}", "{2,}", "*?", "+?", "??", "{1,2}?", "{", "{,2}", "{2,1}"];
  const classItems = ["a", "b", "c", "A", "A-C", "ſ", "K", "Σ-ς", "\\u212a", "ß", "a-c", "x-z", "\\d", "\\w", "\\s", "\\D", "\\S", "\\W", "-", "\\-", "\\b", "\\n", "é", "😀", "\\u0061", "\\x62", "\\cA", "\\p{L}", "\\p{Lu}", "\\P{N}", "1", "0-9", "z-a", "\\d-z", "a-\\d", "\\1", "\\0", "\\c1", "]", "\\]", "^", "\\ud83d", "\\ude00", "\\u{1F600}-\\u{1F64F}", "😀-😎"];
  let names = 0;
  const klass = () => {
    let s = "[" + (rnd() < 0.3 ? "^" : "");
    for (let i = Math.floor(rnd() * 4); i > 0; i--) s += pick(classItems);
    return s + "]";
  };
  const atom = (depth) => {
    const r = rnd();
    if (r < 0.35) return pick(literals);
    if (r < 0.5) return pick(shorthands);
    if (r < 0.6) return klass();
    if (r < 0.65) return pick(assertions);
    if (depth <= 0) return pick(literals);
    const modifiers = ["(?i:", "(?-i:", "(?s:", "(?m:", "(?im-s:", "(?i-:", "(?ims:", "(?m-i:"];
    const open = r < 0.7 ? "(" : r < 0.78 ? pick(modifiers) : r < 0.82 ? "(?:" : r < 0.85 ? "(?=" : r < 0.88 ? "(?!" : r < 0.91 ? "(?<=" : r < 0.93 ? "(?<!" : r < 0.97 ? `(?<n${++names}>` : "(";
    return open + disjunction(depth - 1) + ")";
  };
  const term = (depth) => atom(depth) + (rnd() < 0.3 ? pick(quantifiers) : "");
  const alternative = (depth) => {
    let s = "";
    for (let i = Math.floor(rnd() * 4); i > 0; i--) s += term(depth);
    return s;
  };
  const disjunction = (depth) => {
    let s = alternative(depth);
    while (rnd() < 0.2) s += "|" + alternative(depth);
    return s;
  };
  const seen = new Set();
  const out = [];
  while (out.length < count) {
    names = 0;
    const p = disjunction(3);
    if (p === "" || seen.has(p) || p.includes("\n")) continue;
    seen.add(p);
    out.push(p);
  }
  return out;
}

// randomNamePatterns generates nestings of groups, alternatives and the names n and m, to try how V8 decides which repeated group names are a SyntaxError.
export function randomNamePatterns(seed, count) {
  const rnd = () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 4294967296);
  const pick = (a) => a[Math.floor(rnd() * a.length)];
  const opens = ["(", "(?:", "(?=", "(?<=", "(?<n>", "(?<n>", "(?<m>", "(?<n>", "(?:", "(?!"];
  const disjunction = (depth) => {
    let s = sequence(depth);
    while (rnd() < 0.35) s += "|" + sequence(depth);
    return s;
  };
  const sequence = (depth) => {
    let s = "";
    for (let i = Math.floor(rnd() * 3); i > 0; i--) s += item(depth);
    return s;
  };
  const item = (depth) => (depth <= 0 || rnd() < 0.3 ? "x" : pick(opens) + disjunction(depth - 1) + ")");
  const seen = new Set();
  const out = [];
  while (out.length < count) {
    const p = disjunction(3);
    if (seen.has(p) || (p.match(/\(\?<[nm]>/g) ?? []).length < 2) continue;
    seen.add(p);
    out.push(p);
  }
  return out;
}
