// Helpers shared by the generators: the code point ranges that V8's own property escapes match.
export const SURROGATE_LO = 0xd800;
export const SURROGATE_HI = 0xdfff;
const MAX = 0x10ffff;

// ALL holds every code point except the surrogates, in order. A unit index of ALL maps to a code point with cpAt.
const parts = [];
for (let cp = 0; cp < SURROGATE_LO; cp += 0x1000) parts.push(String.fromCodePoint(...range(cp, Math.min(cp + 0x1000, SURROGATE_LO))));
for (let cp = SURROGATE_HI + 1; cp <= MAX; cp += 0x1000) parts.push(String.fromCodePoint(...range(cp, Math.min(cp + 0x1000, MAX + 1))));
export const ALL = parts.join("");

function range(lo, hi) {
  const out = [];
  for (let c = lo; c < hi; c++) out.push(c);
  return out;
}

function cpAt(unit) {
  if (unit < SURROGATE_LO) return unit;
  if (unit < 0xf800) return unit + 0x800;
  return 0x10000 + (unit - 0xf800) / 2;
}

// rangesOf returns the flat [lo, hi, lo, hi, ...] inclusive ranges of the code points that new RegExp(`\\p{${expr}}`, "u") matches, or null when V8 rejects the expression. Surrogates are not probed: a run that spans them keeps the surrogates.
export function rangesOf(expr) {
  let re;
  try {
    re = new RegExp(`(?:\\p{${expr}})+`, "gu");
  } catch {
    return null;
  }
  const out = [];
  for (const m of ALL.matchAll(re)) {
    const lo = cpAt(m.index);
    const hi = cpAt(m.index + m[0].length) - 1;
    out.push(lo, m.index + m[0].length === ALL.length ? MAX : hi);
  }
  return out;
}

export function sameRanges(a, b) {
  return a.length === b.length && a.every((v, i) => v === b[i]);
}

// withoutSurrogates removes U+D800..U+DFFF from a flat range list, merging runs that touch.
export function withoutSurrogates(flat) {
  const out = [];
  const add = (lo, hi) => {
    if (lo > hi) return;
    if (out.length && out[out.length - 1] + 1 >= lo) out[out.length - 1] = Math.max(out[out.length - 1], hi);
    else out.push(lo, hi);
  };
  for (let i = 0; i < flat.length; i += 2) {
    add(flat[i], Math.min(flat[i + 1], SURROGATE_LO - 1));
    add(Math.max(flat[i], SURROGATE_HI + 1), flat[i + 1]);
  }
  return out;
}

// subtract returns the ranges of a that are not in b; both are sorted flat lists.
export function subtract(a, b) {
  const out = [];
  let j = 0;
  for (let i = 0; i < a.length; i += 2) {
    let lo = a[i];
    const hi = a[i + 1];
    while (j < b.length && b[j + 1] < lo) j += 2;
    let k = j;
    while (lo <= hi) {
      if (k >= b.length || b[k] > hi) {
        out.push(lo, hi);
        break;
      }
      if (b[k] > lo) out.push(lo, b[k] - 1);
      lo = b[k + 1] + 1;
      k += 2;
    }
  }
  return out;
}

// formatRanges is the hex list "41-5a,61,7a-7b" that property_data.go stores a range list as.
export function formatRanges(flat) {
  const parts = [];
  for (let i = 0; i < flat.length; i += 2) parts.push(flat[i] === flat[i + 1] ? flat[i].toString(16) : `${flat[i].toString(16)}-${flat[i + 1].toString(16)}`);
  return parts.join(",");
}
