#!/usr/bin/env node
// Generates chord/next/surfaces/intl_gen.go: what Node's Intl.NumberFormat and Intl.DateTimeFormat print, as tables.
//
// Usage: node automation/gen/generate-intl-tables.mjs > chord/next/surfaces/intl_gen.go   (make intl-tables)
//
// FormatNumber and FormatTime must equal the pinned Node's output (ICU 78.3, CLDR 48 in v24.22.1), which is neither CLDR as
// published (V8 rewrites some patterns) nor what golang.org/x/text knows (CLDR 32, half-even rounding). The script measures
// the oracle instead of copying CLDR: for every locale ICU has data for it derives, from formatToParts, the number symbols
// (decimal, group, sign and affixes, grouping sizes and minimum grouping digits, numbering system digits) and, per closed
// option shape, the time pattern (field order, padding, hour cycle, day period strings). It then replays the same renderer
// that Go runs over a value corpus and fails when it does not reproduce Node byte for byte, so a locale that is not
// pattern-shaped is found here and not at runtime. Which locale a tag resolves to is derived the same way: CLDR's aliases
// plus ECMA-402's BestAvailableLocale over the locales found, with an exceptions table for every tag where that differs.
//
// The closed option shapes mirror surfaces_format.go (what Pi's call sites pass: none, `{timeStyle: "short"}`,
// `{hour: "numeric"}`, fraction digits); the Go drift test runs Node over a large random corpus against the tables.
import process from "node:process";

const U = "ABCDEFGHIJKLMNOPQRSTUVWXYZ";
const L = "abcdefghijklmnopqrstuvwxyz";
const log = (...a) => process.stderr.write(a.join(" ") + "\n");
const ICU = process.versions.icu;

// ---- Which tags exist: aliases and available locales.
const canonical = (tag) => {
  try {
    return Intl.getCanonicalLocales(tag)[0];
  } catch {
    return undefined;
  }
};
const languageAliases = new Map();
const langs = [];
for (const a of L) for (const b of L) {
  langs.push(a + b);
  for (const c of L) langs.push(a + b + c);
}
for (const l of langs) {
  const c = canonical(l);
  if (c !== l) languageAliases.set(l, c);
}
const regions = [];
for (const a of U) for (const b of U) regions.push(a + b);
for (let i = 1; i < 1000; i++) regions.push(String(i).padStart(3, "0"));
const regionAliases = new Map();
for (const r of regions) {
  const c = canonical("und-" + r);
  if (c !== "und-" + r && c.startsWith("und-") && !c.slice(4).includes("-")) regionAliases.set(r, c.slice(4));
}
const scriptAliases = new Map();
const scriptNames = new Intl.DisplayNames("en", { type: "script", fallback: "none" });
const scripts = [];
for (const a of U) for (const b of L) for (const c of L) for (const d of L) {
  const s = a + b + c + d;
  const k = canonical("und-" + s);
  if (k !== "und-" + s && k.startsWith("und-") && !k.slice(4).includes("-")) scriptAliases.set(s, k.slice(4));
  try {
    if (scriptNames.of(s) !== undefined) scripts.push(s);
  } catch {}
}
const regionNames = new Intl.DisplayNames("en", { type: "region", fallback: "none" });
const knownRegions = regions.filter((r) => {
  try {
    return regionNames.of(r) !== undefined;
  } catch {
    return false;
  }
});
const probeRegions = [...new Set([...knownRegions, ...regionAliases.keys(), "ZZ", "AA", "QO"])].sort();
const probeScripts = [...new Set([...scripts, ...scriptAliases.keys()])].sort();

// ---- Tag handling shared with Go (intl_locale.go): split, alias, truncate.
function split(tag) {
  const parts = tag.split("-");
  const out = { lang: parts[0].toLowerCase(), script: "", region: "", variants: [] };
  let i = 1;
  if (parts[i] && /^[A-Za-z]{4}$/.test(parts[i])) out.script = parts[i][0].toUpperCase() + parts[i].slice(1).toLowerCase(), i++;
  if (parts[i] && (/^[A-Za-z]{2}$/.test(parts[i]) || /^[0-9]{3}$/.test(parts[i]))) out.region = parts[i].toUpperCase(), i++;
  for (; i < parts.length; i++) out.variants.push(parts[i].toLowerCase());
  return out;
}
const join = (t) => [t.lang, t.script, t.region, ...t.variants].filter(Boolean).join("-");
function aliased(tag) {
  const t = split(tag);
  const la = languageAliases.get(t.lang);
  if (la !== undefined) {
    const r = split(la);
    t.lang = r.lang;
    if (t.script === "") t.script = r.script;
    if (t.region === "") t.region = r.region;
  }
  if (scriptAliases.has(t.script)) t.script = scriptAliases.get(t.script);
  if (regionAliases.has(t.region)) t.region = regionAliases.get(t.region);
  return join(t);
}
function truncate(avail, tag) {
  let c = tag;
  for (;;) {
    if (avail.has(c)) return c;
    let pos = c.lastIndexOf("-");
    if (pos < 0) return undefined;
    if (pos >= 2 && c[pos - 2] === "-") pos -= 2;
    c = c.slice(0, pos);
  }
}

// ---- A domain: its available locales and the exceptions to the truncation rule.
function domain(name, Ctor, defaults) {
  const resolve = (tag) => {
    try {
      return new Ctor(tag, defaults).resolvedOptions().locale;
    } catch {
      return undefined;
    }
  };
  const deflt = new Ctor(undefined, defaults).resolvedOptions().locale;
  const base = new Set();
  for (const l of langs) {
    if (languageAliases.has(l)) continue;
    const r = resolve(l);
    if (r !== undefined && r.split("-")[0] === l) base.add(l);
  }
  const keys = new Set();
  const results = new Map();
  const probe = (tag) => {
    const k = join(split(tag));
    if (keys.has(k)) return;
    keys.add(k);
    results.set(k, resolve(tag));
  };
  for (const b of base) {
    probe(b);
    for (const r of probeRegions) probe(`${b}-${r}`);
    const own = [];
    for (const s of probeScripts) {
      probe(`${b}-${s}`);
      const r = results.get(join(split(`${b}-${s}`)));
      if (r !== undefined && r.startsWith(`${b}-${s}`)) own.push(s);
    }
    for (const s of own) for (const r of probeRegions) probe(`${b}-${s}-${r}`);
  }
  for (const l of languageAliases.keys()) if (l.length <= 3) probe(l);
  for (const extra of ["en-US-posix", "ca-ES-valencia", "ca-valencia", "de-1996", "de-DE-1901", "en-GB-oxendict", "sr-Latn-RS", "xx", "zzz", "und"]) probe(extra);
  const avail = new Set([...results.values()].filter((v) => v !== undefined));
  avail.add(deflt);
  const exceptions = new Map();
  for (const [k, r] of results) {
    // a tag with no data resolves to the process default locale, which Go reads from the environment at run time
    const mine = truncate(avail, aliased(k)) ?? deflt;
    if (mine !== r) exceptions.set(k, r);
  }
  log(name, "available", avail.size, "probed", keys.size, "exceptions", exceptions.size, "default", deflt);
  return { name, Ctor, avail: [...avail].sort(), exceptions, deflt };
}
const numberDomain = domain("number", Intl.NumberFormat);
const timeDomain = domain("time", Intl.DateTimeFormat, { hour: "numeric" });

// ---- Numbers.
const VALUES = [0, -0, NaN, Infinity, -Infinity, 1, -1, 0.5, -0.5, 1.5, 2.5, -2.5, 0.05, 0.005, 0.0005, 0.00049, 1.0625, 1.0005, 1.005, 8.345, 10, 12, 99, 100, 999, 999.9995, 1000, 1234, 1234.5, -1234.5, 9999, 10000, 12345, 99999, 100000, 123456, 999999, 1000000, 1234567.891, 12345678, 123456789, 1e9, 1234567890123, 1e15, 123456789012345680, 1e21, 1.5e21, 1.2345e25, 1e-7, 5e-324, 1.7976931348623157e308, 0.1, 0.2, 0.30000000000000004, 123.456, 123.4565, 2.675, 1e5 + 0.5, 4503599627370497.5];
const NUMBER_OPTIONS = [{}, { maximumFractionDigits: 0 }, { minimumFractionDigits: 2 }, { maximumFractionDigits: 1 }, { minimumFractionDigits: 1, maximumFractionDigits: 2 }, { minimumFractionDigits: 3, maximumFractionDigits: 3 }, { minimumFractionDigits: 0, maximumFractionDigits: 20 }, { minimumFractionDigits: 6 }, { minimumFractionDigits: 2, maximumFractionDigits: 100 }, { maximumFractionDigits: 100 }];

// The renderer Go runs (intl_number.go): the shortest decimal of the double, rounded half away from zero, grouped, in the
// locale's digits and affixes.
function renderNumber(f, value, minFD, maxFD) {
  const negative = value < 0 || Object.is(value, -0);
  if (Number.isNaN(value)) return f.nan;
  const [pre, suf] = negative ? [f.negPrefix, f.negSuffix] : [f.posPrefix, f.posSuffix];
  if (!Number.isFinite(value)) return pre + f.inf + suf;
  let int = "0", frac = "";
  if (value !== 0) {
    const [m, e] = Math.abs(value).toExponential().split("e");
    const digits = m.replace(".", "");
    const point = Number(e) + 1;
    if (point >= digits.length) int = digits + "0".repeat(point - digits.length);
    else if (point > 0) (int = digits.slice(0, point)), (frac = digits.slice(point));
    else frac = "0".repeat(-point) + digits;
  }
  if (frac.length > maxFD) {
    const up = frac[maxFD] >= "5";
    frac = frac.slice(0, maxFD);
    if (up) {
      const all = (int + frac).split("");
      let i = all.length - 1;
      while (i >= 0 && all[i] === "9") all[i--] = "0";
      if (i < 0) all.unshift("1");
      else all[i] = String(Number(all[i]) + 1);
      const str = all.join("");
      int = str.slice(0, str.length - frac.length);
      frac = str.slice(str.length - frac.length);
    }
  }
  int = int.replace(/^0+(?=.)/, "");
  frac = frac.replace(/0+$/, "");
  frac = frac.padEnd(minFD, "0");
  if (f.primary > 0 && int.length >= f.primary + f.minGroup) {
    const groups = [int.slice(-f.primary)];
    for (let rest = int.slice(0, -f.primary); rest.length > 0; rest = rest.slice(0, -f.secondary)) groups.unshift(rest.slice(-f.secondary));
    int = groups.join(f.group);
  }
  let body = int + (frac === "" ? "" : f.decimal + frac);
  const zero = f.zero.codePointAt(0);
  body = [...body].map((c) => (c >= "0" && c <= "9" ? String.fromCodePoint(zero + Number(c)) : c)).join("");
  return pre + body + suf;
}
const unzero = (text, zero) => [...text].map((c) => (c.codePointAt(0) >= zero && c.codePointAt(0) <= zero + 9 ? String(c.codePointAt(0) - zero) : c)).join("");

function numberFormatOf(locale) {
  const fmt = new Intl.NumberFormat(locale);
  const parts = (v) => fmt.formatToParts(v);
  const big = parts(1234567890000.5);
  const lastInt = big.filter((p) => p.type === "integer").pop().value;
  const zero = [...lastInt].pop().codePointAt(0);
  const f = { zero: String.fromCodePoint(zero), decimal: "", group: "", nan: fmt.format(NaN), inf: "", posPrefix: "", posSuffix: "", negPrefix: "", negSuffix: "", primary: 0, secondary: 0, minGroup: 1 };
  f.decimal = big.find((p) => p.type === "decimal").value;
  const affixes = (v) => {
    const ps = parts(v);
    const first = ps.findIndex((p) => ["integer", "infinity"].includes(p.type));
    let last = -1;
    ps.forEach((p, i) => {
      if (["integer", "group", "decimal", "fraction", "infinity"].includes(p.type)) last = i;
    });
    return [ps.slice(0, first).map((p) => p.value).join(""), ps.slice(last + 1).map((p) => p.value).join("")];
  };
  [f.posPrefix, f.posSuffix] = affixes(1.5);
  [f.negPrefix, f.negSuffix] = affixes(-1.5);
  // dz prints Infinity as an "integer" part, so the symbol is what is left of the text between the affixes.
  f.inf = fmt.format(Infinity).slice(f.posPrefix.length, fmt.format(Infinity).length - f.posSuffix.length);
  const groupPart = big.find((p) => p.type === "group");
  if (groupPart !== undefined) {
    f.group = groupPart.value;
    const segs = big.filter((p) => p.type === "integer").map((p) => [...p.value].length).reverse();
    f.primary = segs[0];
    f.secondary = segs.length > 2 ? segs[1] : segs[0];
    let min = 0;
    for (let k = 1; k <= 16 && min === 0; k++) if (parts(10 ** (k - 1)).some((p) => p.type === "group")) min = k;
    f.minGroup = min === 0 ? 1 : min - f.primary;
  }
  return f;
}

const numberFormats = new Map(); // locale -> format record
const failures = [];
for (const locale of numberDomain.avail) {
  const f = numberFormatOf(locale);
  numberFormats.set(locale, f);
  for (const o of NUMBER_OPTIONS) {
    const fmt = new Intl.NumberFormat(locale, o);
    const minFD = o.minimumFractionDigits ?? 0;
    const maxFD = o.maximumFractionDigits ?? Math.max(minFD, 3);
    for (const v of VALUES) {
      const want = fmt.format(v);
      const got = renderNumber(f, v, minFD, maxFD);
      if (want !== got) failures.push(`number ${locale} ${JSON.stringify(o)} ${Object.is(v, -0) ? "-0" : v}: node ${JSON.stringify(want)} model ${JSON.stringify(got)}`);
    }
  }
}
if (failures.length > 0) {
  log(failures.slice(0, 40).join("\n"), `\n${failures.length} number failures`);
  process.exit(1);
}
log("numbers verified:", numberDomain.avail.length, "locales x", NUMBER_OPTIONS.length, "options x", VALUES.length, "values");

// ---- Times. A shape is (timeStyle, hour, minute, second, hour12); the slot is the mixed-radix number below (surfaces).
const STYLES = [undefined, "short", "medium"];
const FIELDS = [undefined, "numeric", "2-digit"];
const H12 = [undefined, true, false];
const shapeSlots = [];
for (let st = 0; st < 3; st++) for (let h = 0; h < 3; h++) for (let m = 0; m < 3; m++) for (let s = 0; s < 3; s++) for (let t = 0; t < 3; t++) {
  const fields = h + m + s > 0;
  if (st > 0 === fields) continue; // a style excludes fields (a TypeError in Intl); neither would print a date
  shapeSlots.push({ slot: (((st * 3 + h) * 3 + m) * 3 + s) * 3 + t, st, h, m, s, t });
}
const optionsOf = (sh) => {
  const o = { timeZone: "UTC" };
  if (sh.st) o.timeStyle = STYLES[sh.st];
  if (sh.h) o.hour = FIELDS[sh.h];
  if (sh.m) o.minute = FIELDS[sh.m];
  if (sh.s) o.second = FIELDS[sh.s];
  if (sh.t) o.hour12 = H12[sh.t];
  return o;
};
// Pattern template bytes: 1/2 hour (bare/padded), 3/4 minute, 5/6 second, 7 day period; everything else is literal. The
// day period is a function of the minute of the day: am and pm, or CLDR's flexible periods (zh-Hant: 凌晨, 清晨, 上午, 中午,
// ...), kept as the minutes at which the text changes.
const CYCLES = ["", "h11", "h12", "h23", "h24"];
function periodAt(periods, minuteOfDay) {
  let text = periods[0].text;
  for (const q of periods) if (q.start <= minuteOfDay) text = q.text;
  return text;
}
function renderTime(p, zero, hour, minute, second) {
  const cycle = CYCLES[p.cycle];
  const h = cycle === "h12" ? hour % 12 || 12 : cycle === "h11" ? hour % 12 : cycle === "h24" ? hour || 24 : hour;
  const num = (n, pad) => String(n).padStart(pad ? 2 : 1, "0");
  let out = "";
  for (const c of p.tmpl) {
    const k = c.charCodeAt(0);
    out += k === 1 ? num(h, false) : k === 2 ? num(h, true) : k === 3 ? num(minute, false) : k === 4 ? num(minute, true) : k === 5 ? num(second, false) : k === 6 ? num(second, true) : k === 7 ? periodAt(p.periods, hour * 60 + minute) : c;
  }
  const z = zero.codePointAt(0);
  return [...out].map((c) => (c >= "0" && c <= "9" ? String.fromCodePoint(z + Number(c)) : c)).join("");
}
const instant = (h, m, s) => new Date(Date.UTC(2026, 0, 1, h, m, s));

function timePatternOf(locale, zero, sh) {
  const fmt = new Intl.DateTimeFormat(locale, optionsOf(sh));
  const at = (h) => fmt.formatToParts(instant(h, 5, 7));
  const text = (parts) => parts.map((p) => p.value).join("");
  const nine = at(9), two = at(14), zeroH = at(0);
  const p = { cycle: 0, tmpl: "", periods: [] };
  const hasPeriod = nine.some((q) => q.type === "dayPeriod");
  const value = (q) => Number(unzero(q.value, zero.codePointAt(0)));
  if (sh.st ? nine.some((q) => q.type === "hour") : sh.h) {
    const d0 = value(zeroH.find((q) => q.type === "hour"));
    // The cycle is not always visible as a day period: es-AR's `hh:mm:ss` counts to 12 and shows no am/pm.
    const reported = CYCLES.indexOf(fmt.resolvedOptions().hourCycle);
    p.cycle = reported > 0 ? reported : hasPeriod ? (d0 === 12 ? 2 : 1) : d0 === 24 ? 4 : 3;
  }
  if (hasPeriod) {
    // sampled at the hour, the minute after, the half hour and the last minute; the verification below checks the rest
    for (let h = 0; h < 24; h++) for (const m of [0, 1, 30, 59]) {
      const text = fmt.formatToParts(instant(h, m, 0)).find((q) => q.type === "dayPeriod").value;
      if (p.periods.length === 0 || p.periods[p.periods.length - 1].text !== text) p.periods.push({ start: h * 60 + m, text });
    }
    if (p.periods[0].start !== 0) throw new Error("periods do not start at midnight");
  }
  for (const q of nine) {
    const padded = [...q.value].length === 2;
    switch (q.type) {
      case "hour": p.tmpl += padded ? "\x02" : "\x01"; break;
      case "minute": p.tmpl += padded ? "\x04" : "\x03"; break;
      case "second": p.tmpl += padded ? "\x06" : "\x05"; break;
      case "dayPeriod": p.tmpl += "\x07"; break;
      case "literal": p.tmpl += q.value; break;
      default: throw new Error(`${locale}: part ${q.type} in ${JSON.stringify(text(nine))}`);
    }
  }
  // formatToParts and format() do not always print the same text: V8 turns ICU's U+202F (narrow no-break space) into a plain
  // space in format() (the one Pi calls), and some locales' strings differ in Unicode normalization. Take the variant of
  // the pattern that format() agrees with.
  const variants = [(t) => t, (t) => t.replaceAll("\u202f", " "), (t) => t.normalize("NFC"), (t) => t.normalize("NFD"), (t) => t.replaceAll("\u202f", " ").normalize("NFC"), (t) => t.replaceAll("\u202f", " ").normalize("NFD")];
  const base = { ...p };
  const sample = [[9, 5, 7], [14, 5, 7], [0, 5, 7], [5, 5, 7], [12, 0, 0]].map(([h, m, s]) => fmt.format(instant(h, m, s))).join("|");
  let fits = false;
  for (const v of variants) {
    p.tmpl = v(base.tmpl);
    p.periods = base.periods.map((q) => ({ start: q.start, text: v(q.text) }));
    if ([[9, 5, 7], [14, 5, 7], [0, 5, 7], [5, 5, 7], [12, 0, 0]].map(([h, m, s]) => renderTime(p, zero, h, m, s)).join("|") === sample) {
      fits = true;
      break;
    }
  }
  if (!fits) Object.assign(p, base);
  if (/[\x01-\x07]/.test([...nine].filter((q) => q.type === "literal").map((q) => q.value).join(""))) throw new Error(`${locale}: control character in a literal`);
  return { p, fmt };
}
const HMS = [[5, 7], [30, 45], [16, 28], [39, 59], [0, 0], [9, 10]];
const patternKey = (p) => JSON.stringify([p.cycle, p.tmpl, p.periods]);
const patterns = [], patternIndex = new Map();
const rows = [], rowIndex = new Map();
const timeLocales = [];
let checked = 0;
for (const locale of timeDomain.avail) {
  const zeroProbe = new Intl.DateTimeFormat(locale, { minute: "2-digit", second: "2-digit", timeZone: "UTC" }).formatToParts(instant(0, 0, 0)).find((q) => q.type === "minute").value;
  const zero = String.fromCodePoint([...zeroProbe][0].codePointAt(0));
  const row = [];
  for (const sh of shapeSlots) {
    const { p, fmt } = timePatternOf(locale, zero, sh);
    for (let h = 0; h < 24; h++) for (const [m, s] of HMS) {
      const want = fmt.format(instant(h, m, s));
      const got = renderTime(p, zero, h, m, s);
      checked++;
      if (want !== got) failures.push(`time ${locale} ${JSON.stringify(optionsOf(sh))} ${h}:${m}:${s}: node ${JSON.stringify(want)} model ${JSON.stringify(got)}`);
    }
    const key = patternKey(p);
    if (!patternIndex.has(key)) (patternIndex.set(key, patterns.length), patterns.push(p));
    row.push(patternIndex.get(key));
  }
  const rkey = row.join(",");
  if (!rowIndex.has(rkey)) (rowIndex.set(rkey, rows.length), rows.push(row));
  timeLocales.push({ locale, row: rowIndex.get(rkey), zero: zero.codePointAt(0) });
}
if (failures.length > 0) {
  log(failures.slice(0, 40).join("\n"), `\n${failures.length} failures`);
  process.exit(1);
}
log("times verified:", checked, "formats;", patterns.length, "patterns,", rows.length, "rows,", timeLocales.length, "locales");

// ---- Emit.
const q = (s) => {
  let out = '"';
  for (const c of s) {
    const k = c.codePointAt(0);
    if (c === '"' || c === "\\") out += "\\" + c;
    else if (k >= 0x20 && k < 0x7f) out += c;
    else if (k < 0x10000) out += "\\u" + k.toString(16).padStart(4, "0");
    else out += "\\U" + k.toString(16).padStart(8, "0");
  }
  return out + '"';
};
const sortedPairs = (map) => [...map].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
const pairs = (name, map) => `var ${name} = [...]localePair{\n${sortedPairs(map).map(([a, b]) => `\t{${q(a)}, ${q(b)}},\n`).join("")}}\n\n`;
const strings = (name, list) => `var ${name} = [...]string{\n${list.map((s) => `\t${q(s)},\n`).join("")}}\n\n`;

const numberRecords = [], numberRecordIndex = new Map();
const numberData = numberDomain.avail.map((locale) => {
  const f = numberFormats.get(locale);
  const key = JSON.stringify(f);
  if (!numberRecordIndex.has(key)) (numberRecordIndex.set(key, numberRecords.length), numberRecords.push(f));
  return numberRecordIndex.get(key);
});

let out = `// Code generated by automation/gen/generate-intl-tables.mjs from Node ${process.version} (ICU ${ICU}, CLDR ${process.versions.cldr}); DO NOT EDIT.

package surfaces

const intlOracleICU = ${q(ICU)}

`;
out += pairs("intlLanguageAliases", languageAliases);
out += pairs("intlScriptAliases", scriptAliases);
out += pairs("intlRegionAliases", regionAliases);
out += strings("intlNumberLocales", numberDomain.avail);
out += pairs("intlNumberExceptions", numberDomain.exceptions);
out += `var intlNumberLocaleFormat = [...]uint16{${numberData.join(", ")}}\n\n`;
out += `var intlNumberFormats = [...]numberFormat{\n${numberRecords.map((f) => `\t{zero: ${f.zero.codePointAt(0)}, decimal: ${q(f.decimal)}, group: ${q(f.group)}, nan: ${q(f.nan)}, inf: ${q(f.inf)}, posPrefix: ${q(f.posPrefix)}, posSuffix: ${q(f.posSuffix)}, negPrefix: ${q(f.negPrefix)}, negSuffix: ${q(f.negSuffix)}, primary: ${f.primary}, secondary: ${f.secondary}, minGroup: ${f.minGroup}},\n`).join("")}}\n\n`;
out += strings("intlTimeLocales", timeDomain.avail);
out += pairs("intlTimeExceptions", timeDomain.exceptions);
out += `var intlTimeLocaleData = [...]timeLocale{\n${timeLocales.map((t) => `\t{row: ${t.row}, zero: ${t.zero}},\n`).join("")}}\n\n`;
out += `// intlTimeShapeSlots are the valid shape slots in ascending order: the columns of intlTimeRows.\nvar intlTimeShapeSlots = [...]uint8{${shapeSlots.map((s) => s.slot).join(", ")}}\n\n`;
out += `var intlTimePatterns = [...]timePattern{\n${patterns.map((p) => `\t{cycle: ${p.cycle}, tmpl: ${q(p.tmpl)}, periods: []dayPeriod{${p.periods.map((d) => `{${d.start}, ${q(d.text)}}`).join(", ")}}},\n`).join("")}}\n\n`;
out += `var intlTimeRows = [...][${shapeSlots.length}]uint16{\n${rows.map((r) => `\t{${r.join(", ")}},\n`).join("")}}\n`;
await new Promise((done) => process.stdout.write(out, done));
