// Generates corpus.jsonl for the differential test against Chord 1.0.4's overlay tracker.
// Each line: {"base": <JSON>, "steps": [...], "ops": <emitted Op[]>, "value": <JSON of prepared value>}.
// Run: node testdata/gen.mjs > testdata/corpus.jsonl  (CHORD defaults to the gate's Pi main checkout, Node 26 type stripping)
const { track } = await import(process.env.CHORD ?? `${process.env.CONTRACT_PI_SOURCE ?? new URL("../../../contract/.cache/pi-main", import.meta.url).pathname}/packages/chord/src/delta/index.ts`);

let seed = Number(process.env.SEED ?? 7);
const rnd = () => { seed = (seed * 1664525 + 1013904223) >>> 0; return seed / 4294967296; };
const ri = (n) => Math.floor(rnd() * n);
const pick = (a) => a[ri(a.length)];
const KEYS = ["a", "b", "c", "items", "text", "n", "0", "1", "x y", "role", "constructor"];
const STRS = ["", "a", "hello", "hello world", "héllo", "😀", "ab😀cd", "tab\t", "\ud800", "x".repeat(40)];
function scalar() {
  const k = rnd();
  if (k < 0.1) return null;
  if (k < 0.2) return rnd() < 0.5;
  if (k < 0.55) return pick([0, 1, 2, -1, 1.5, 100, 1e21, 0.1]);
  return pick(STRS);
}
function value(d) {
  const r = rnd();
  if (d > 2 || r < 0.5) return scalar();
  if (r < 0.75) return Array.from({ length: ri(4) }, () => value(d + 1));
  const o = {};
  for (let i = ri(4); i > 0; i--) o[pick(KEYS.filter((k) => k !== "__proto__"))] = value(d + 1);
  return o;
}
function rootDoc() {
  const o = {};
  for (let i = 1 + ri(5); i > 0; i--) o[pick(KEYS.filter((k) => k !== "__proto__" && k !== "constructor"))] = value(0);
  return o;
}
const clone = (v) => JSON.parse(JSON.stringify(v));
// Walk the shadow value to every container path.
function containers(v, path = [], out = []) {
  if (v !== null && typeof v === "object") {
    out.push([path, v]);
    for (const k of Object.keys(v)) containers(v[k], [...path, Array.isArray(v) ? Number(k) : k], out);
  }
  return out;
}
function derive(cur) {
  if (typeof cur === "string" && rnd() < 0.6) {
    const k = ri(4);
    if (k === 0) return cur + pick(STRS);
    if (k === 1) return cur.slice(ri(cur.length + 1));
    if (k === 2) return cur.slice(ri(3)) + "tail";
    return cur.slice(0, 3) + cur.slice(2);
  }
  return value(1);
}
function step(shadow) {
  const cs = containers(shadow);
  const [path, node] = pick(cs);
  if (Array.isArray(node)) {
    const k = ri(8);
    if (k === 0) return { path, op: "push", values: Array.from({ length: 1 + ri(2) }, () => value(1)) };
    if (k === 1 && node.length) return { path, op: "pop" };
    if (k === 2 && node.length) return { path, op: "shift" };
    if (k === 3) return { path, op: "unshift", values: [value(1)] };
    if (k === 4) { const start = ri(node.length + 1); return { path, op: "splice", start, remove: ri(node.length - start + 1), items: Array.from({ length: ri(3) }, () => value(1)) }; }
    if (k === 5 && node.length) { const index = ri(node.length); return { path, op: "setIndex", index, value: derive(node[index]) }; }
    if (k === 6) return { path, op: "setIndex", index: node.length, value: value(1) };
    return { path, op: "push", values: [scalar()] };
  }
  const k = ri(5);
  const keys = Object.keys(node);
  if (k === 0 && keys.length) return { path, op: "delete", key: pick(keys) };
  const key = k < 3 && keys.length ? pick(keys) : pick(KEYS);
  return { path, op: "set", key, value: key in node ? derive(node[key]) : value(1) };
}
function applyTo(root, s) {
  let t = root;
  for (const seg of s.path) t = t[seg];
  switch (s.op) {
    case "push": return t.push(...clone(s.values));
    case "pop": return t.pop();
    case "shift": return t.shift();
    case "unshift": return t.unshift(...clone(s.values));
    case "splice": return t.splice(s.start, s.remove, ...clone(s.items));
    case "setIndex": t[s.index] = clone(s.value); return;
    case "delete": delete t[s.key]; return;
    case "set": t[s.key] = clone(s.value); return;
  }
}
if (process.env.MODE === "big") {
  // Large arrays (dense regions), many operations (the 4,096 fold) and long strings (the overlap search).
  for (let i = 0; i < Number(process.env.N ?? 40); i++) {
    const len = 300 + ri(500);
    const base = { list: Array.from({ length: len }, (_, k) => (rnd() < 0.5 ? k : { v: k, s: "s" + k })), text: "ab".repeat(8000 + ri(1000)), other: { deep: [1, 2, 3] } };
    const shadow = clone(base);
    const tr = track(clone(base));
    const change = tr.beginChange();
    const steps = [];
    const edits = ri(6) === 0 ? 5000 : 100 + ri(900);
    for (let e = 0; e < edits; e++) {
      const idx = ri(len);
      let s;
      const k = ri(10);
      if (k < 6) s = { path: ["list"], op: "setIndex", index: idx, value: rnd() < 0.5 ? -k : { v: -k } };
      else if (k < 8 && typeof shadow.list[idx] === "object" && shadow.list[idx] !== null) s = { path: ["list", idx], op: "set", key: "v", value: e };
      else if (k < 9 && rnd() < 0.05) s = { path: [], op: "set", key: "text", value: shadow.text.slice(ri(5)) + "zz" + ri(100) };
      else s = { path: ["other", "deep"], op: "push", values: [e] };
      applyTo(shadow, s); applyTo(change.state, s); steps.push(s);
    }
    const prepared = change.prepare();
    console.log(JSON.stringify({ base, steps, ops: prepared.ops, value: prepared.value }));
  }
  process.exit(0);
}
const out = [];
const N = Number(process.env.N ?? 2500);
for (let i = 0; i < N; i++) {
  const base = rootDoc();
  const shadow = clone(base);
  const steps = [];
  const tr = track(clone(base));
  const change = tr.beginChange();
  let ok = true;
  for (let n = 1 + ri(6); n > 0 && ok; n--) {
    const s = step(shadow);
    try { applyTo(shadow, s); applyTo(change.state, s); steps.push(s); } catch { ok = false; }
  }
  if (!ok) { change.abort(); continue; }
  let prepared;
  try { prepared = change.prepare(); } catch { continue; }
  const sv = JSON.stringify(shadow);
  const pv = JSON.stringify(prepared.value);
  if (sv !== pv) { process.stderr.write(`shadow/value mismatch (key order?) at ${i}\n${sv}\n${pv}\n`); }
  out.push(JSON.stringify({ base, steps, ops: prepared.ops, value: prepared.value }));
}
console.log(out.join("\n"));
