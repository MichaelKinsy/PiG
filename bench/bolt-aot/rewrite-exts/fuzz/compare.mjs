// SPDX-License-Identifier: MIT
// Compares three rewrites of one corpus against the expected output the generator wrote (gen.mjs): the Piglet's rewrite as it is now, as it was before the second review, and
// Pi-Bolt's rewriteCopy (run here, from the file named by the last argument). Run with Bun: it also parses every output with Bun's transpiler.
// usage: bun compare.mjs CORPUS.json HAND.json NEW.json OLD.json PIBOLT/gen-plugin-manifest.mjs [--repros N]
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

const [corpusPath, handPath, newPath, oldPath, upstreamPath] = process.argv.slice(2, 7);
const J = JSON.stringify;
const repros = Number(process.argv[process.argv.indexOf("--repros") + 1] || 12);
const { rewriteCopy } = await import(pathToFileURL(upstreamPath).href);

const corpus = JSON.parse(readFileSync(corpusPath, "utf8"));
const hand = JSON.parse(readFileSync(handPath, "utf8"));
const items = corpus.concat(hand);
const ours = JSON.parse(readFileSync(newPath, "utf8"));
const old = JSON.parse(readFileSync(oldPath, "utf8"));
if (ours.length !== items.length || old.length !== items.length) throw new Error(`outputs (${ours.length}, ${old.length}) do not match the ${items.length} items: run the Go side on the same corpus and hand file`);

const loader = (file) => ({ ts: "ts", mts: "ts", cts: "ts", tsx: "tsx", jsx: "jsx", js: "jsx", mjs: "jsx", cjs: "jsx" })[file.split(".").pop()];
const parses = (code, file) => { try { new Bun.Transpiler({ loader: loader(file) }).transformSync(code); return true; } catch { return false; } };

const impls = { ours: (i) => ours[i], old: (i) => old[i], upstream: (i) => rewriteCopy(items[i].src, items[i].file) };
const wrong = { ours: [], old: [], upstream: [] }, invalid = { ours: [], old: [], upstream: [] };
const outputs = { ours: [], old: [], upstream: [] };
items.forEach((item, i) => {
  const expParses = parses(item.exp, item.file);
  for (const [name, run] of Object.entries(impls)) {
    const out = run(i);
    outputs[name][i] = out;
    if (out !== item.exp) wrong[name].push(i);
    if (expParses && !parses(out, item.file)) invalid[name].push(i);
  }
});

const kinds = ["module", "fragment", "hand"];
const count = (list, kind) => list.filter((i) => items[i].kind === kind).length;
console.log(`corpus: ${kinds.map((k) => `${items.filter((i) => i.kind === k).length} ${k}s`).join(", ")}`);
for (const name of Object.keys(impls)) {
  console.log(`${name.padEnd(8)} wrong output: ${wrong[name].length} (${kinds.map((k) => `${count(wrong[name], k)} ${k}`).join(", ")}); expected parses but the output does not: ${invalid[name].length}`);
}
const oldAndUpstreamIdentical = wrong.old.filter((i) => outputs.old[i] === outputs.upstream[i]);
console.log(`old (the OLD_TREE rewrite) wrong: ${wrong.old.length}; of them byte-identical in Pi-Bolt's output: ${oldAndUpstreamIdentical.length}`);
const oursOnlyWrongNewly = wrong.ours.filter((i) => !wrong.old.includes(i));
console.log(`ours wrong now but right before: ${oursOnlyWrongNewly.length}`);

// By family: how many single fragments each implementation gets wrong, and the minimal repros of Pi-Bolt's.
const byTag = {};
for (const i of items.keys()) {
  const item = items[i];
  if (item.kind === "module") continue;
  const tag = item.kind === "hand" ? "hand" : item.tags[0];
  byTag[tag] ??= { total: 0, ours: 0, old: 0, upstream: 0, same: 0 };
  byTag[tag].total++;
  for (const name of Object.keys(impls)) if (wrong[name].includes(i)) byTag[tag][name]++;
  if (wrong.old.includes(i) && outputs.old[i] === outputs.upstream[i]) byTag[tag].same++;
}
console.log("\nsingle fragments and hand cases by family (wrong outputs / total):");
for (const [tag, v] of Object.entries(byTag)) console.log(`  ${tag.padEnd(12)} ours ${v.ours}/${v.total}  old ${v.old}/${v.total}  upstream ${v.upstream}/${v.total}  (old wrong and byte-identical in Pi-Bolt: ${v.same})`);

const show = (title, list, impl, other) => {
  console.log(`\n${title}`);
  const seen = new Set();
  for (const i of list) {
    const item = items[i];
    if (item.kind === "module" || outputs[impl][i] === outputs[other]?.[i]) continue;
    const key = item.tags[0] + "|" + item.src.replace(/\d+/g, "N").slice(0, 40);
    if (seen.has(key)) continue;
    seen.add(key);
    if (seen.size > repros) break;
    console.log(`- [${item.file.split(".").pop()}] ${J(item.src)}\n    expected ${J(item.exp)}\n    ${impl.padEnd(8)} ${J(outputs[impl][i])}`);
  }
};
show("Pi-Bolt wrong where ours is right (minimal repros, single fragments):", wrong.upstream.filter((i) => !wrong.ours.includes(i)), "upstream");
show("ours wrong (must be empty):", wrong.ours, "ours");
process.exit(wrong.ours.length === 0 && invalid.ours.length === 0 ? 0 : 1);
