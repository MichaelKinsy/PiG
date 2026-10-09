// Drive the installed Pi chord delta module, never a translated oracle: seeded random apply, diffRevisions and encoder/decoder cases with Pi's results.
import { readFileSync, realpathSync } from "node:fs";
import { pathToFileURL } from "node:url";
const root = realpathSync(new URL("../../../extensions/sdk-ts/node_modules/@earendil-works/pi-coding-agent/node_modules/@earendil-works/chord/", import.meta.url)) + "/";
if (JSON.parse(readFileSync(root + "package.json", "utf8")).version !== process.argv[2]) throw new Error("Wrong Pi oracle version");
const { apply, applyImmutable, diffRevisions, encoder, decoder } = await import(pathToFileURL(root + "dist/delta/index.js"));
const count = Number(process.argv[3]);
let seed = Number(process.argv[4]);
// mulberry32
const rnd = () => {
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};
const pick = (xs) => xs[Math.floor(rnd() * xs.length)];
const strs = ["", "héllo", "😀x", "ab", "a😀b", "hello world", "hello there world", "abcdefghijklmnopqrstuvwxyz", "abcdefghijXYZnopqrstuvwxyz"];
const paths = (v, prefix = [], out = []) => {
  out.push(prefix);
  if (Array.isArray(v)) v.forEach((x, i) => paths(x, [...prefix, i], out));
  else if (v && typeof v === "object") for (const k of Object.keys(v)) paths(v[k], [...prefix, k], out);
  return out;
};
const at = (v, p) => p.reduce((x, k) => x[k], v);
const outcome = (f) => {
  try {
    return { ok: JSON.parse(JSON.stringify(f() ?? null)) };
  } catch (e) {
    return { error: e.constructor.name, message: e.message };
  }
};

// apply and applyImmutable over valid and invalid operations.
function applyCases() {
  const keys = ["a", "b", "c", "0", "1", "length", "x y"];
  const value = (depth) => {
    const r = rnd();
    if (depth <= 0 || r < 0.35) return pick([null, true, false, 0, 1, -2.5, 1e21, ...strs.slice(0, 5)]);
    if (r < 0.65) return Array.from({ length: Math.floor(rnd() * 4) }, () => value(depth - 1));
    const o = {};
    for (let n = Math.floor(rnd() * 4); n > 0; n--) o[pick(keys)] = value(depth - 1);
    return o;
  };
  const int = () => (rnd() < 0.8 ? pick([0, 1, 2]) : pick([-2, -1, 3, 10, 1.5]));
  const seg = () => (rnd() < 0.85 ? pick(["a", "b", "c", 0, 1, "length"]) : pick(["__proto__", "x y", "0", 5, -1, 1.5, 2]));
  const path = (target, nonEmpty, want = "") => {
    const all = paths(target);
    const typed = all.filter((q) => (want === "array" ? Array.isArray(at(target, q)) : typeof at(target, q) === "string"));
    const pool = want !== "" && typed.length > 0 && rnd() < 0.8 ? typed : all;
    let p;
    if (rnd() < 0.8) {
      p = [...pick(pool)];
      if (rnd() < 0.3 && want === "") p.push(seg());
    } else p = Array.from({ length: 1 + Math.floor(rnd() * 3) }, seg);
    if (nonEmpty && p.length === 0) p.push(seg());
    return p;
  };
  const op = (target) => {
    switch (pick(["r", "s", "s", "d", "d", "a", "t", "t", "p", "p", "m", "r", "s", "a", "t", "d", "p", "bad"])) {
      case "r": return ["r", value(2)];
      case "s": return ["s", path(target, true), value(2)];
      case "d": return ["d", path(target, true)];
      case "a": return ["a", path(target, true, "string"), pick(strs)];
      case "t": return ["t", path(target, true, "string"), int()];
      case "p": return ["p", path(target, false, "array"), int(), int(), Array.from({ length: Math.floor(rnd() * 3) }, () => value(1))];
      case "m": {
        const n = Math.floor(rnd() * 4);
        const order = Array.from({ length: n }, (_, i) => i).sort(() => rnd() - 0.5);
        if (rnd() < 0.3 && n > 0) order[0] = pick([n, -1, 0, 1.5]);
        return ["m", path(target, false, "array"), order];
      }
      default:
        return pick([["s", [], 1], ["x", ["a"]], ["s", ["a"]], ["p", ["a"], 0, 0], ["t", ["a"], "1"], ["a", ["a"], 1], ["r"], [], ["d", [true]]]);
    }
  };
  const cases = [];
  for (let i = 0; i < count; i++) {
    const target = value(3);
    const ops = Array.from({ length: rnd() < 0.6 ? 1 : 2 + Math.floor(rnd() * 2) }, () => op(target));
    const text = JSON.stringify(target);
    const copy = () => [JSON.parse(text), JSON.parse(JSON.stringify(ops))];
    cases.push({ target, ops, mutable: outcome(() => apply(...copy())), immutable: outcome(() => applyImmutable(...copy())) });
  }
  return cases;
}

// diffRevisions between a value and a mutated revision. Object keys are inserted in random order, array-index keys among them, and the mutations append new keys, so the operation order follows own-key order. No array is empty: an empty Go slice has no identity to compare by reference. No string holds a lone surrogate, which a Go string cannot.
function diffCases() {
  const keys = ["a", "b", "c", "x", "y", "length", "a b", "2", "10"];
  const value = (depth) => {
    const r = rnd();
    if (depth <= 0 || r < 0.3) return pick([null, true, false, 0, 1, -2.5, 3, ...strs]);
    if (r < 0.65) return Array.from({ length: 1 + Math.floor(rnd() * 5) }, () => value(depth - 1));
    const o = {};
    for (let n = Math.floor(rnd() * 4); n > 0; n--) o[pick(keys)] = value(depth - 1);
    return o;
  };
  const mutate = (v) => {
    const all = paths(v).filter((p) => p.length > 0);
    for (let tries = 0; tries < 10; tries++) {
      const p = all.length > 0 ? pick(all) : [];
      const current = p.length > 0 ? at(v, p) : v;
      const r = rnd();
      let op;
      if (Array.isArray(current) && r < 0.4) {
        const start = Math.floor(rnd() * (current.length + 1));
        const removed = Math.floor(rnd() * (current.length - start + 1));
        op = rnd() < 0.3 && current.length > 1 ? ["m", p, [...current.keys()].reverse()] : ["p", p, start, removed, Array.from({ length: Math.floor(rnd() * 3) }, () => value(1))];
      } else if (typeof current === "string" && r < 0.5 && p.length > 0) op = rnd() < 0.5 ? ["a", p, pick(strs)] : ["t", p, Math.floor(rnd() * (current.length + 1))];
      else if (current && typeof current === "object" && !Array.isArray(current) && r < 0.6) op = ["s", [...p, pick(keys)], value(1)];
      else if (p.length > 0) op = rnd() < 0.5 ? ["d", p] : ["s", p, value(2)];
      else continue;
      try {
        return applyImmutable(v, [op]);
      } catch {}
    }
    return v;
  };
  const cases = [];
  while (cases.length < count) {
    const before = value(3);
    let after = before;
    if (rnd() < 0.1) after = value(3);
    else for (let n = 1 + Math.floor(rnd() * 4); n > 0; n--) after = mutate(after);
    const text = JSON.stringify([before, after]);
    if (text.includes("[]") || /\\ud[89a-f]/i.test(text)) continue;
    // The Go side decodes both revisions from text, so they share no container; the copies keep key order and share none either.
    const [left, right] = JSON.parse(text);
    cases.push({ before: left, after: right, ops: diffRevisions(left, right) });
  }
  return cases;
}

// One encoder and decoder per stream; some wire batches carry an invalid operation, which the decoder must reject with Pi's error and state.
function codecStreams() {
  const segs = ["a", "b", "1", 1, 0, "é", "😀", 'a"b', 2];
  const path = (min) => Array.from({ length: min + Math.floor(rnd() * 3) }, () => pick(segs));
  const val = () => pick([null, 1, "x", [1], { k: 2 }, true, -0.5]);
  const op = () => {
    switch (pick(["r", "s", "d", "a", "t", "p", "m"])) {
      case "r": return ["r", val()];
      case "s": return ["s", path(1), val()];
      case "d": return ["d", path(1)];
      case "a": return ["a", path(1), pick(["", "z", "😀"])];
      case "t": return ["t", path(1), pick([0, 1, 2])];
      case "p": return ["p", path(0), pick([0, 1]), pick([0, 1]), [val()]];
      default: return ["m", path(0), pick([[0], [1, 0], []])];
    }
  };
  const badWire = () => pick([["#", 0, ["a"]], ["#", 99, []], ["s", 5, 1], ["s", 1], ["d"], ["p", 0, 0, []], ["m", [0]], ["#", -1, ["a"]], ["#", 1.5, ["a"]], ["x"], ["s", ["__proto__"], 1], ["a", "q"], ["t", 1], ["#", 0, "a"]]);
  const streams = [];
  for (let stream = 0; stream < Math.ceil(count / 5); stream++) {
    const enc = encoder();
    const dec = decoder();
    const steps = [];
    let previous = path(1);
    for (let batch = 1 + Math.floor(rnd() * 8); batch > 0; batch--) {
      const ops = [];
      for (let n = 1 + Math.floor(rnd() * 5); n > 0; n--) {
        const next = op();
        if (next[0] !== "r" && rnd() < 0.2) next[1] = previous;
        if (next[0] !== "r") previous = next[1];
        if (next[0] === "r" || next[0] === "p" || next[0] === "m" || next[1].length > 0) ops.push(next);
      }
      let wire;
      try {
        wire = enc.encode(ops);
      } catch (e) {
        wire = { error: e.message };
      }
      const encoded = JSON.parse(JSON.stringify(wire));
      if (Array.isArray(wire) && rnd() < 0.15) {
        wire = [...wire];
        wire.splice(Math.floor(rnd() * (wire.length + 1)), 0, badWire());
      }
      let decoded = null;
      if (Array.isArray(wire)) {
        try {
          decoded = dec.decode(JSON.parse(JSON.stringify(wire)));
        } catch (e) {
          decoded = { error: `${e.constructor.name}: ${e.message}` };
        }
      }
      steps.push({ ops, encoded, wire, decoded });
    }
    streams.push(steps);
  }
  return streams;
}

process.stdout.write(JSON.stringify({ apply: applyCases(), diff: diffCases(), codec: codecStreams() }));
